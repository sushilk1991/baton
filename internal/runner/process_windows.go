package runner

import (
	"io"
	"os"
	"os/exec"
	"syscall"
)

const (
	createNewProcessGroup = 0x00000200
	interruptExit         = 130
)

func prepareGroup(command *exec.Cmd, _ io.Reader) func() error {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
	return func() error { return nil }
}

func forwardSignals() []os.Signal          { return []os.Signal{os.Interrupt} }
func terminateSignal() os.Signal           { return os.Interrupt }
func signalCode(os.Signal) int             { return interruptExit }
func childCode(state *os.ProcessState) int { return state.ExitCode() }

func signalGroup(command *exec.Cmd, _ os.Signal) error {
	// Windows console delivery is best effort; terminate the owned command
	// when no Unix process-group signal primitive exists.
	return command.Process.Kill()
}
