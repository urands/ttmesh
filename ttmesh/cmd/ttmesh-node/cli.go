package main

import "flag"

// Options holds CLI options for the node.
type Options struct {
    ConfigPath string
}

// ParseFlags parses CLI flags from args and returns Options.
func ParseFlags(args []string) Options {
    fs := flag.NewFlagSet("ttmesh-node", flag.ExitOnError)
    var opts Options
    fs.StringVar(&opts.ConfigPath, "config", "", "Path to YAML config file")
    _ = fs.Parse(args)
    return opts
}

