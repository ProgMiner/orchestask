package ssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

import (
	"golang.org/x/crypto/ssh"
)

import (
	"bypm.ru/orchestask/internal/model"
	"bypm.ru/orchestask/internal/service"
	"bypm.ru/orchestask/internal/util"
)

const (
	sshPublicKeyPermission = "pkey"
)

type SSHServer struct {
	containerUsername string

	service   *service.Service
	sshConfig *ssh.ServerConfig
	listener  net.Listener

	resourcesLock sync.Mutex
	resources     []io.Closer
}

type sshConn struct {
	conn           *ssh.ServerConn
	forwardChans   []ssh.NewChannel
	forwardReqs    []*ssh.Request
	remainingChans <-chan ssh.NewChannel
	remainingReqs  <-chan *ssh.Request
	session        ssh.Channel
	sessionReqs    <-chan *ssh.Request
}

func MakeSSHServer(
	service *service.Service,
	host, keyFile, containerUsername string,
) (*SSHServer, error) {
	srv := &SSHServer{service: service, containerUsername: containerUsername}

	privateBytes, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load private key: %w", err)
	}

	private, err := ssh.ParsePrivateKey(privateBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	srv.listener, err = net.Listen("tcp", host)
	if err != nil {
		return nil, fmt.Errorf("unable to run SSH server: %w", err)
	}

	srv.addResource(srv.listener)

	srv.sshConfig = &ssh.ServerConfig{PublicKeyCallback: srv.publicKeyCallback}
	srv.sshConfig.AddHostKey(private)
	return srv, nil
}

func (srv *SSHServer) publicKeyCallback(
	conn ssh.ConnMetadata,
	key ssh.PublicKey,
) (*ssh.Permissions, error) {
	perms := &ssh.Permissions{
		Extensions: map[string]string{sshPublicKeyPermission: ssh.FingerprintSHA256(key)},
	}

	return perms, nil
}

func (srv *SSHServer) Run(ctx context.Context) error {
	closing := make(chan struct{})

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig

		fmt.Printf("Shutting down...\n")

		close(closing)
		if err := srv.listener.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Unable to close SSH server: %v\n", err)
		}
	}()

	fmt.Printf("SSH server started on %s\n", srv.listener.Addr().String())

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)
	ctx = util.WithLoggingScope(ctx, "SSH")

	defer func() {
		_ = srv.Close()
		cancel()

		wg.Wait()
	}()

	for {
		conn, err := srv.listener.Accept()
		if err != nil {
			select {
			case <-closing:
				return nil
			default:
				return err
			}
		}

		wg.Add(1)
		go srv.handleConn(ctx, &wg, conn)
	}
}

func (srv *SSHServer) handleConn(ctx context.Context, wg *sync.WaitGroup, conn net.Conn) {
	defer wg.Done()

	ctx = util.WithLoggingScope(ctx, conn.RemoteAddr().String())
	if err := srv._handleConn(ctx, wg, conn); err != nil {
		util.Log(ctx, "SSH communication error: %v", err)
	}
}

func (srv *SSHServer) _handleConn(ctx context.Context, wg *sync.WaitGroup, conn net.Conn) (res error) {
	defer func() {
		if err := recover(); err != nil {
			res = fmt.Errorf("panic: %v", err)
		}
	}()

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, srv.sshConfig)
	if err != nil {
		srv.addResource(conn)
		return fmt.Errorf("unable to establish connection: %w", err)
	}

	srv.addResource(sshConn)

	mySSHConn, err := srv.initSSHConn(ctx, sshConn, chans, reqs)
	if err != nil {
		return fmt.Errorf("unable to initialize connection: %w", err)
	}

	if err := srv.handleSSHConn(ctx, wg, mySSHConn); err != nil {
		_, _ = fmt.Fprint(mySSHConn.session.Stderr(), "internal server error\r\n")
		mySSHConn.rejectAll(wg)
		return err
	}

	return
}

func (srv *SSHServer) handleSSHConn(ctx context.Context, wg *sync.WaitGroup, conn *sshConn) error {
	// TODO: add some kind of transaction on user...

	username := conn.conn.User()
	pkey := conn.conn.Permissions.Extensions[sshPublicKeyPermission]

	util.Log(ctx, "New connection: %s %s", username, pkey)

	sshUser, err := srv.service.User.Authenticate(pkey)
	if err != nil {
		return fmt.Errorf("unable to authenticate SSH user with public key %s: %w", pkey, err)
	}

	if sshUser == nil {
		sshUser, err = srv.service.User.Register(pkey)
		if err != nil {
			return fmt.Errorf("unable to register SSH user with public key %s: %w", pkey, err)
		}

		_, _ = fmt.Fprintf(conn.session, "Hello, %s! You have been registered.\r\n", username)
	}

	if sshUser.TG == 0 {
		tgLink, err := model.IDToString(sshUser.ID)
		if err != nil {
			return err
		}

		tgLink, err = srv.service.TGBot.MakeStartLink(ctx, tgLink)
		if err != nil {
			return fmt.Errorf("unable to generate TG link for %s: %w", sshUser.ID, err)
		}

		msg := "Seems like you haven't attached your Telegram account for now. " +
			"To do that, follow the link:\r\n" +
			"\t%s\r\n" +
			"\r\n" +
			"I will wait until you do that <(^_^)>\r\n"

		_, _ = fmt.Fprintf(conn.session, msg, tgLink)

		sshUserID := sshUser.ID
		sshUser, err = srv.service.User.WaitTGAttached(ctx, sshUserID)
		if err != nil {
			return fmt.Errorf("unable to wait for TG attach for %s: %w", sshUserID, err)
		}

		util.Log(ctx, "Attached SSH user %s to TG %d", sshUserID, sshUser.ID)

		_, _ = fmt.Fprint(conn.session, "Great!\r\n")
	}

	user, err := srv.service.User.GetByID(sshUser.TG)
	if err != nil {
		return fmt.Errorf("unable to resolve user by ID %d: %w", sshUser.TG, err)
	}

	ctx = util.WithLoggingScope(ctx, fmt.Sprintf("%d", user.ID))
	util.Log(ctx, "Logged in using SSH user %s", sshUser.ID)

	if user.Container == "" {
		_, _ = fmt.Fprint(conn.session, "Initializing container for you...\r\n")

		hostname := user.Username
		if hostname == "" {
			hostname = username
		}

		containerImage, containerID, err := srv.service.Docker.InitContainer(ctx, hostname, user.ContainerImage)
		if err != nil {
			return fmt.Errorf("unable to initialize container: %w", err)
		}

		user, err = srv.service.User.UpdateContainer(user.ID, containerImage, containerID)
		if err != nil {
			return fmt.Errorf("unable to set container ID: %w", err)
		}

		util.Log(ctx, `Initialized container from image "%s": %s`, containerImage, containerID)
	}

	containerIP, err := srv.service.Docker.EnsureContainer(ctx, user.Container)
	if err != nil {
		return fmt.Errorf("unable to ensure running container: %w", err)
	}

	util.Log(ctx, "Connecting to container IP %s...", containerIP)

	containerAddr := fmt.Sprintf("%s:22", containerIP)

	// container could be just started by EnsureContainer so try several times
	var connectionErr error
	for range 3 {
		clientConn, clientChans, clientReqs, err := srv.connectContainer(containerAddr, conn.session)
		if err != nil {
			connectionErr = err

			time.Sleep(1 * time.Second)
			continue
		}

		return srv.redirectConn(conn, clientConn, clientChans, clientReqs)
	}

	return fmt.Errorf("unable to connect container by SSH: %w", connectionErr)
}

func (srv *SSHServer) connectContainer(
	addr string,
	w io.Writer,
) (ssh.Conn, <-chan ssh.NewChannel, <-chan *ssh.Request, error) {
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, nil, nil, err
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, &ssh.ClientConfig{
		User: srv.containerUsername,
		Auth: []ssh.AuthMethod{
			ssh.Password(""),
			ssh.KeyboardInteractive(func(_, _ string, _ []string, _ []bool) ([]string, error) {
				return nil, nil
			}),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		BannerCallback: func(message string) error {
			_, err := fmt.Fprintf(w, "%s\r\n", message)
			return err
		},
	})

	if err != nil {
		return nil, nil, nil, err
	}

	srv.addResource(sshConn)
	return sshConn, chans, reqs, nil
}

func (srv *SSHServer) initSSHConn(
	ctx context.Context,
	conn *ssh.ServerConn,
	chans <-chan ssh.NewChannel,
	reqs <-chan *ssh.Request,
) (*sshConn, error) {
	res := &sshConn{conn: conn, remainingChans: chans, remainingReqs: reqs}

loop:
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		case ch := <-chans:
			if ch.ChannelType() == "session" {
				var err error

				res.session, res.sessionReqs, err = ch.Accept()
				if err != nil {
					return nil, fmt.Errorf("unable to accept session channel: %w", err)
				}

				srv.addResource(res.session)
				break loop
			}

			res.forwardChans = append(res.forwardChans, ch)

		case r := <-reqs:
			res.forwardReqs = append(res.forwardReqs, r)

		case <-time.After(10 * time.Second):
			return nil, fmt.Errorf("client hasn't requested session channel in time")
		}
	}

	return res, nil
}

func (conn *sshConn) rejectAll(wg *sync.WaitGroup) {
	for _, ch := range conn.forwardChans {
		_ = ch.Reject(ssh.ConnectionFailed, "internal server error")
	}

	for _, r := range conn.forwardReqs {
		if r.WantReply {
			_ = r.Reply(false, nil)
		}
	}

	wg.Add(3)

	go func() {
		defer wg.Done()
		ssh.DiscardRequests(conn.sessionReqs)
	}()

	go func() {
		defer wg.Done()

		for ch := range conn.remainingChans {
			_ = ch.Reject(ssh.ConnectionFailed, "internal server error")
		}
	}()

	go func() {
		defer wg.Done()
		ssh.DiscardRequests(conn.remainingReqs)
	}()
}

// TODO: error collecting
func (srv *SSHServer) redirectConn(
	conn *sshConn,
	connTo ssh.Conn,
	chans <-chan ssh.NewChannel,
	reqs <-chan *ssh.Request,
) error {
	session, sessionReqs, err := connTo.OpenChannel("session", nil)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup

	srv.redirectChan(&wg, session, conn.session, sessionReqs, conn.sessionReqs)

	wg.Add(4)

	go func() {
		defer wg.Done()

		for ch := range conn.remainingChans {
			_ = srv.redirectNewChan(&wg, ch, connTo)
		}

		_ = connTo.Close()
	}()

	go func() {
		defer wg.Done()

		for ch := range chans {
			_ = srv.redirectNewChan(&wg, ch, conn.conn)
		}

		_ = conn.conn.Close()
	}()

	go func() {
		defer wg.Done()

		for r := range conn.remainingReqs {
			_ = srv.redirectReq(r, connTo.SendRequest)
		}

		_ = connTo.Close()
	}()

	go func() {
		defer wg.Done()

		for r := range reqs {
			_ = srv.redirectReq(r, conn.conn.SendRequest)
		}

		_ = conn.conn.Close()
	}()

	for _, ch := range conn.forwardChans {
		if err := srv.redirectNewChan(&wg, ch, connTo); err != nil {
			return err
		}
	}

	for _, r := range conn.forwardReqs {
		if err := srv.redirectReq(r, connTo.SendRequest); err != nil {
			return err
		}
	}

	wg.Wait()
	return nil
}

func (srv *SSHServer) redirectNewChan(wg *sync.WaitGroup, newChan ssh.NewChannel, conn ssh.Conn) error {
	chan1, reqs1, err := conn.OpenChannel(newChan.ChannelType(), newChan.ExtraData())
	if err != nil {
		_ = newChan.Reject(ssh.ConnectionFailed, "unable to connect container")
		return err
	}

	chan2, reqs2, err := newChan.Accept()
	if err != nil {
		_ = chan1.Close()
		return err
	}

	srv.addResource(chan1)
	srv.addResource(chan2)

	srv.redirectChan(wg, chan1, chan2, reqs1, reqs2)
	return nil
}

func (srv *SSHServer) redirectChan(
	wg *sync.WaitGroup,
	chan1, chan2 ssh.Channel,
	reqs1, reqs2 <-chan *ssh.Request,
) {
	wg.Add(2)

	go func() {
		defer wg.Done()

		defer func() {
			_ = chan2.Close()
		}()

		srv.redirectChanReqs(reqs1, chan2)
	}()

	go func() {
		defer wg.Done()

		defer func() {
			_ = chan1.Close()
		}()

		srv.redirectChanReqs(reqs2, chan1)
	}()

	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = io.Copy(chan1, chan2)
	}()

	go func() {
		defer wg.Done()
		_, _ = io.Copy(chan2, chan1)
	}()

	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = io.Copy(chan1.Stderr(), chan2.Stderr())
	}()

	go func() {
		defer wg.Done()
		_, _ = io.Copy(chan2.Stderr(), chan1.Stderr())
	}()
}

func (srv *SSHServer) redirectChanReqs(
	from <-chan *ssh.Request,
	to ssh.Channel,
) {
	srv.redirectReqs(from, func(name string, wantReply bool, payload []byte) (bool, []byte, error) {
		res, err := to.SendRequest(name, wantReply, payload)
		return res, nil, err
	})
}

func (srv *SSHServer) redirectReqs(
	from <-chan *ssh.Request,
	to func(name string, wantReply bool, payload []byte) (bool, []byte, error),
) {
	for r := range from {
		_ = srv.redirectReq(r, to)
	}
}

func (srv *SSHServer) redirectReq(
	from *ssh.Request,
	to func(name string, wantReply bool, payload []byte) (bool, []byte, error),
) error {
	res, payload, err := to(from.Type, from.WantReply, from.Payload)

	if from.WantReply {
		if err1 := from.Reply(res, payload); err == nil {
			err = err1
		}
	}

	return err
}

func (srv *SSHServer) addResource(res io.Closer) {
	srv.resourcesLock.Lock()

	defer srv.resourcesLock.Unlock()

	srv.resources = append(srv.resources, res)
}

func (srv *SSHServer) Close() error {
	srv.resourcesLock.Lock()

	defer srv.resourcesLock.Unlock()

	errs := []error{}
	for _, res := range srv.resources {
		if err := res.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}
