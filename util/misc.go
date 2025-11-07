package util

import (
	"os"
	"runtime"
	"sync"
)

func GetenvOr(key, value string) string {
	if res, ok := os.LookupEnv(key); ok {
		return res
	}

	return value
}

func PrintTraces() {
	buf := make([]byte, 1024*1024)
	buf = buf[:runtime.Stack(buf, true)]
	_, _ = os.Stderr.Write(buf)
}

func Synchronized[T any, E any](lock sync.Locker, f func() (T, E)) (T, E) {
	lock.Lock()

	defer lock.Unlock()
	return f()
}
