package ui

import (
	"flag"
	"io"
	"log"

	"k8s.io/klog/v2"
)

func init() {
	// Silence klog output from client-go (like SPDY executor EOF errors when exiting shells)
	fs := flag.NewFlagSet("klog", flag.ContinueOnError)
	klog.InitFlags(fs)
	_ = fs.Set("logtostderr", "false")
	klog.SetOutput(io.Discard)
	log.SetOutput(io.Discard) // safety net for raw log calls inside SPDY
}
