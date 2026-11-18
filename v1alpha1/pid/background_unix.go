//go:build unix

package pid

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"

	v1 "github.com/tunnel-pizza/tunneld/v1"
)

// Background is -d's start: exe run with args and env in a session
// of its own, stdin on /dev/null, this process's stdout and stderr, and a
// status pipe, told to signal this process once it is up. It returns once the
// run signals, leaving it running. A run that ends first is an ExitError with
// its status; one refused because the same run is going answers that run's
// Status with an ExitError of RunningCode. An interrupt while it comes up ends
// it, and is answered 128 plus the signal, as a shell does.
func Background(exe string, args, env []string) (Status, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return Status{}, err
	}
	defer r.Close()

	up := make(chan os.Signal, 1)
	signal.Notify(up, syscall.SIGUSR2)
	defer signal.Stop(up)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(stop)

	cmd := exec.Command(exe, args...)
	cmd.Env = append(env, v1.NotifyPidEnv+"="+strconv.Itoa(os.Getpid()), v1.StatusFdEnv+"=3")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.ExtraFiles = []*os.File{w}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		w.Close()
		return Status{}, err
	}
	w.Close()

	status := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(r)
		status <- b
	}()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	for {
		select {
		case <-up:
			return Status{Pid: cmd.Process.Pid}, nil
		case sig := <-stop:
			_ = cmd.Process.Signal(os.Interrupt)
			<-exited
			return Status{}, v1.ExitError{Code: 128 + int(sig.(syscall.Signal)), Err: fmt.Errorf("interrupted before the run was up")}
		case err := <-exited:
			code := cmd.ProcessState.ExitCode()
			if code == v1.RunningCode {
				var s Status
				if json.Unmarshal(<-status, &s) == nil && s.Pid > 0 {
					return s, v1.ExitError{Code: v1.RunningCode, Err: fmt.Errorf("%w as pid %d", v1.ErrRunning, s.Pid)}
				}
			}
			if code < 0 {
				code = 1
			}
			return Status{}, v1.ExitError{Code: code, Err: fmt.Errorf("the run ended before it was up: %v", err)}
		}
	}
}
