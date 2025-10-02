package main

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
	// "bypm.ru/orchestask/storage"
	"bypm.ru/orchestask/service"
)

const (
	sshPublicKeyPermission = "pubkey"
)

type sshServer struct {
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

func makeSSHServer(
	service *service.Service,
	host, keyFile, containerUsername string,
) (*sshServer, error) {
	srv := &sshServer{service: service, containerUsername: containerUsername}

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

func (srv *sshServer) publicKeyCallback(
	conn ssh.ConnMetadata,
	key ssh.PublicKey,
) (*ssh.Permissions, error) {
	perms := &ssh.Permissions{
		Extensions: map[string]string{sshPublicKeyPermission: ssh.FingerprintSHA256(key)},
	}

	return perms, nil
}

func (srv *sshServer) run(ctx context.Context) error {
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

func (srv *sshServer) handleConn(ctx context.Context, wg *sync.WaitGroup, conn net.Conn) {
	defer wg.Done()

	if err := srv._handleConn(ctx, wg, conn); err != nil {
		fmt.Fprintf(os.Stderr, "[%s] SSH communication error: %v\n", conn.RemoteAddr().String(), err)
	}
}

func (srv *sshServer) _handleConn(ctx context.Context, wg *sync.WaitGroup, conn net.Conn) (res error) {
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

func (srv *sshServer) handleSSHConn(ctx context.Context, wg *sync.WaitGroup, conn *sshConn) error {

	username := conn.conn.User()
	pkey := conn.conn.Permissions.Extensions[sshPublicKeyPermission]

	addr := conn.conn.RemoteAddr().String()
	fmt.Printf("[%s] %s %s\n", addr, username, pkey)

	user, err := srv.service.User.Identify(username, pkey)
	if err != nil {
		return fmt.Errorf("unable to identify user: %w", err)
	}

	if user == nil {
		user, err = srv.service.User.Register(username, pkey)
		if err != nil {
			return fmt.Errorf("unable to register user: %w", err)
		}

		_, _ = fmt.Fprintf(conn.session, "Hello, %s! You have been just registered.\r\n", username)
	}

	fmt.Printf("[%s] [%s] logged in\n", addr, user.ID)

	if user.TG == 0 {
		user, err = srv.service.User.UpdateTGLink(user.ID)
		if err != nil {
			return fmt.Errorf("unable to generate TG link: %w", err)
		}

		tgLink, err := srv.service.TGBot.MakeStartLink(ctx, user.TGLink)
		if err != nil {
			return fmt.Errorf("unable to generate TG link: %w", err)
		}

		msg := "Seems like you haven't attached your Telegram account for now. " +
			"To do that, follow the link:\r\n" +
			"\t%s\r\n" +
			"\r\n" +
			"I will wait until you do that <(^_^)>\r\n"

		_, _ = fmt.Fprintf(conn.session, msg, tgLink)

		user, err = srv.service.User.WaitTGAttached(ctx, user.ID)
		if err != nil {
			return fmt.Errorf("unable to wait for TG attach: %w", err)
		}

		_, _ = fmt.Fprint(conn.session, "Great!\r\n")
	}

	if user.Container == "" {
		_, _ = fmt.Fprint(conn.session, "Initializing container for you...\r\n")

		hostname := user.TGUsername
		if hostname == "" {
			hostname = user.SSHUsername
		}

		containerID, err := srv.service.Docker.InitContainer(ctx, hostname)
		if err != nil {
			return fmt.Errorf("unable to initialize container: %w", err)
		}

		user, err = srv.service.User.UpdateContainer(user.ID, containerID)
		if err != nil {
			return fmt.Errorf("unable to set container ID: %w", err)
		}
	}

	containerIP, err := srv.service.Docker.EnsureContainer(ctx, user.Container)
	if err != nil {
		return fmt.Errorf("unable to ensure running container: %w", err)
	}

	fmt.Printf("[%s] [%s] Connecting to container IP %s\n", addr, user.ID, containerIP)

	containerAddr := fmt.Sprintf("%s:22", containerIP)
	clientConn, clientChans, clientReqs, err := srv.connectContainer(containerAddr, conn.session)
	if err != nil {
		return fmt.Errorf("unable to connect container by SSH: %w", err)
	}

	return srv.redirectConn(conn, clientConn, clientChans, clientReqs)
}

func (srv *sshServer) connectContainer(
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

func (srv *sshServer) initSSHConn(
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

			fmt.Printf("forward channel: %s\n", ch.ChannelType())
			res.forwardChans = append(res.forwardChans, ch)

		case r := <-reqs:
			fmt.Printf("forward request: %s\n", r.Type)
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

// TODO: error collection
func (srv *sshServer) redirectConn(
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

func (srv *sshServer) redirectNewChan(wg *sync.WaitGroup, newChan ssh.NewChannel, conn ssh.Conn) error {
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

func (srv *sshServer) redirectChan(
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

func (srv *sshServer) redirectChanReqs(
	from <-chan *ssh.Request,
	to ssh.Channel,
) {
	srv.redirectReqs(from, func(name string, wantReply bool, payload []byte) (bool, []byte, error) {
		res, err := to.SendRequest(name, wantReply, payload)
		return res, nil, err
	})
}

func (srv *sshServer) redirectReqs(
	from <-chan *ssh.Request,
	to func(name string, wantReply bool, payload []byte) (bool, []byte, error),
) {
	for r := range from {
		_ = srv.redirectReq(r, to)
	}
}

func (srv *sshServer) redirectReq(
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

func (srv *sshServer) addResource(res io.Closer) {
	srv.resourcesLock.Lock()

	defer srv.resourcesLock.Unlock()

	srv.resources = append(srv.resources, res)
}

func (srv *sshServer) Close() error {
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
