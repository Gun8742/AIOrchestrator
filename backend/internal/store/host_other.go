//go:build !darwin || !cgo

package store

func hostMachineID() string { return "" }
