package main

import "os"

func stopSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}

func terminateSignal() os.Signal {
	return os.Kill
}

func killed(error) bool {
	return false
}
