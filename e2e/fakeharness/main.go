// Command fakeharness plays a harness process for the e2e tests: it runs one command as
// its child, the way a harness runs a hook, prints "exit <code>" when the child is done,
// and then keeps running until it is killed, or until the process named by
// ABOARD_EXIT_WITH_PID (the test that started it) has exited.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: fakeharness <command> [args...]")
		os.Exit(2)
	}
	cmd := exec.CommandContext(context.Background(), os.Args[1], os.Args[2:]...) //nolint:gosec // runs the command the test gives it
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stderr, os.Stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			fmt.Fprintln(os.Stderr, "fakeharness:", err)
			os.Exit(2)
		}
		code = exit.ExitCode()
	}
	fmt.Printf("exit %d\n", code)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	owner, _ := strconv.Atoi(os.Getenv("ABOARD_EXIT_WITH_PID"))
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			if owner > 0 && errors.Is(syscall.Kill(owner, 0), syscall.ESRCH) {
				return
			}
		}
	}
}
