package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(runWithContext(ctx, os.Args[1:], os.Stdout, os.Stderr, http.DefaultClient))
}

func runWithContext(ctx context.Context, args []string, stdout, stderr io.Writer, httpClient *http.Client) int {
	global := flag.NewFlagSet("wendy", flag.ContinueOnError)
	global.SetOutput(stderr)
	configPath := global.String("config", defaultConfigPath, "config file path")
	global.StringVar(configPath, "c", defaultConfigPath, "config file path")
	if err := global.Parse(args); err != nil {
		return 2
	}
	remaining := global.Args()
	if len(remaining) == 0 {
		printUsage(stderr)
		return 2
	}

	switch remaining[0] {
	case "init":
		return runInit(*configPath, remaining[1:], stdout, stderr)
	case "up":
		return runUp(ctx, *configPath, remaining[1:], stdout, stderr)
	case "primary":
		return runPrimaryCommand(ctx, *configPath, remaining[1:], stdout, stderr)
	case "node":
		return runNodeCommand(ctx, *configPath, remaining[1:], stdout, stderr)
	case "provider":
		return runProviderCommand(ctx, *configPath, remaining[1:], stdout, stderr)
	case "status":
		cfg, ok := loadConfigForCommand(*configPath, stderr)
		if !ok {
			return 1
		}
		return runStatus(cfg, stdout, httpClient)
	case "tools":
		cfg, ok := loadConfigForCommand(*configPath, stderr)
		if !ok {
			return 1
		}
		return runTools(cfg, stdout, stderr, httpClient)
	case "invoke":
		cfg, ok := loadConfigForCommand(*configPath, stderr)
		if !ok {
			return 1
		}
		return runInvoke(cfg, remaining[1:], stdout, stderr, httpClient)
	case "artifacts":
		cfg, ok := loadConfigForCommand(*configPath, stderr)
		if !ok {
			return 1
		}
		return runArtifacts(cfg, remaining[1:], stdout, stderr, httpClient)
	case "admin":
		cfg, ok := loadConfigForCommand(*configPath, stderr)
		if !ok {
			return 1
		}
		return runAdmin(cfg, remaining[1:], stdout, stderr)
	default:
		printUsage(stderr)
		fmt.Fprintf(stderr, "unknown command %q\n", remaining[0])
		return 2
	}
}

func runInit(configPath string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	force := flags.Bool("force", false, "overwrite an existing config")
	profile := flags.String("profile", "local", "config profile: local or distributed")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *profile != "local" && *profile != "distributed" {
		fmt.Fprintln(stderr, "--profile must be local or distributed")
		return 2
	}
	cfg, err := defaultConfig()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *profile == "distributed" {
		cfg.Primary.Host = "primary-host"
		cfg.Primary.BindHost = "0.0.0.0"
		cfg.Providers[0].Kind = "comfyui"
		cfg.Providers[0].ServiceID = "svc_generic_gpu_image"
		cfg.Providers[0].ServiceName = "Generic GPU Image Provider"
		cfg.Providers[0].NodeID = "node_gpu"
		cfg.Providers[0].CapabilityID = "cap_image_generate"
		cfg.Providers[0].Addr = "0.0.0.0:18088"
		cfg.Providers[0].Endpoint = "http://gpu-host:18088"
		cfg.Providers[0].DryRun = true
		cfg.Nodes[0].NodeID = "node_gpu"
		cfg.Nodes[0].DisplayName = "GPU Node"
		cfg.Nodes[0].Addr = "0.0.0.0:18087"
		cfg.Nodes[0].PublicURL = "http://gpu-host:18087"
		cfg.Nodes[0].Resources[0].ResourceID = "res_gpu_0"
		cfg.Nodes[0].Resources[0].DisplayName = "GPU"
		cfg.Nodes[0].Resources[0].Tags = []string{"gpu"}
	}
	if err := writeConfig(configPath, cfg, *force); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	_ = ensureGeneratedIgnore(filepath.Dir(configPath))
	fmt.Fprintf(stdout, "created %s\n", configPath)
	return 0
}

func runUp(ctx context.Context, configPath string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("up", flag.ContinueOnError)
	flags.SetOutput(stderr)
	noRunner := flags.Bool("no-runner", false, "start without the embedded runner")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	cfg, created, err := ensureConfig(configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if created {
		fmt.Fprintf(stdout, "created %s\n", configPath)
	}
	fmt.Fprintf(stdout, "gateway: %s\n", cfg.gatewayURL())
	fmt.Fprintf(stdout, "try: wendy tools\n")
	if err := runConfiguredStack(ctx, cfg, stackOptions{StartProvider: true, StartNodes: true, DisableRunner: *noRunner}); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runPrimaryCommand(ctx context.Context, configPath string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "up" {
		fmt.Fprintln(stderr, "usage: wendy primary up")
		return 2
	}
	cfg, ok := loadConfigForCommand(configPath, stderr)
	if !ok {
		return 1
	}
	fmt.Fprintf(stdout, "primary gateway: %s\n", cfg.gatewayURL())
	if err := runConfiguredStack(ctx, cfg, stackOptions{StartProvider: false}); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runNodeCommand(ctx context.Context, configPath string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "up" {
		fmt.Fprintln(stderr, "usage: wendy node up --node <node-id>")
		return 2
	}
	flags := flag.NewFlagSet("node up", flag.ContinueOnError)
	flags.SetOutput(stderr)
	nodeID := flags.String("node", "", "node id to start")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if *nodeID == "" {
		fmt.Fprintln(stderr, "--node is required")
		return 2
	}
	cfg, ok := loadConfigForCommand(configPath, stderr)
	if !ok {
		return 1
	}
	fmt.Fprintf(stdout, "node: %s\n", *nodeID)
	if err := runNodeRole(ctx, cfg, *nodeID, nil); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runProviderCommand(ctx context.Context, configPath string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "up" {
		fmt.Fprintln(stderr, "usage: wendy provider up --service <service-id>")
		return 2
	}
	flags := flag.NewFlagSet("provider up", flag.ContinueOnError)
	flags.SetOutput(stderr)
	serviceID := flags.String("service", "", "provider service id to start")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if *serviceID == "" {
		fmt.Fprintln(stderr, "--service is required")
		return 2
	}
	cfg, ok := loadConfigForCommand(configPath, stderr)
	if !ok {
		return 1
	}
	fmt.Fprintf(stdout, "provider: %s\n", *serviceID)
	if err := runProviderRole(ctx, cfg, *serviceID, nil); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runAdmin(cfg Config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: wendy admin credentials [--show]")
		return 2
	}
	switch args[0] {
	case "credentials":
		flags := flag.NewFlagSet("admin credentials", flag.ContinueOnError)
		flags.SetOutput(stderr)
		show := flags.Bool("show", false, "show credential values")
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		values := map[string]any{
			"agent":      redacted(cfg.Credentials.Agent, *show),
			"component":  redacted(cfg.Credentials.Component, *show),
			"runner":     redacted(cfg.Credentials.Runner, *show),
			"node_admin": redacted(cfg.Credentials.NodeAdmin, *show),
		}
		return writeJSON(stdout, map[string]any{"ok": true, "data": values})
	case "health":
		return runStatus(cfg, stdout, http.DefaultClient)
	default:
		fmt.Fprintf(stderr, "unknown admin command %q\n", args[0])
		return 2
	}
}

func redacted(value string, show bool) string {
	if show {
		return value
	}
	if value == "" {
		return ""
	}
	return "[redacted]"
}

func loadConfigForCommand(path string, stderr io.Writer) (Config, bool) {
	cfg, err := loadConfig(path)
	if err != nil {
		fmt.Fprintf(stderr, "load %s: %v\n", path, err)
		return Config{}, false
	}
	return cfg, true
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: wendy [-c wendy.yaml] <command>")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "commands:")
	fmt.Fprintln(w, "  init [--profile local|distributed] [--force]")
	fmt.Fprintln(w, "  up [--no-runner]")
	fmt.Fprintln(w, "  primary up")
	fmt.Fprintln(w, "  node up --node <node-id>")
	fmt.Fprintln(w, "  provider up --service <service-id>")
	fmt.Fprintln(w, "  status")
	fmt.Fprintln(w, "  tools")
	fmt.Fprintln(w, "  invoke <capability-id> [--input JSON] [--wait]")
	fmt.Fprintln(w, "  artifacts <job-id> [--out-dir dir]")
	fmt.Fprintln(w, "  admin credentials [--show]")
}
