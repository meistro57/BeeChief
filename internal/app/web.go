package app

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"

	"github.com/meistro57/beechief/internal/config"
	"github.com/meistro57/beechief/internal/jobs"
	"github.com/meistro57/beechief/internal/report"
	"github.com/meistro57/beechief/internal/web"
)

func runWebCLI(cfgPath string, cfg config.Config, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("addr", "127.0.0.1:8080", "listen address (host:port)")
	jobsFile := fs.String("jobs", jobs.DefaultPath(), "jobs YAML file")
	allowRemote := fs.Bool("allow-remote", false, "allow binding a non-loopback address")
	readOnly := fs.Bool("read-only", false, "disable config edits, service control, instructions, and job runs")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("web flags: %w", err)
	}

	host, port, err := net.SplitHostPort(*addr)
	if err != nil {
		return fmt.Errorf("web: invalid --addr %q: %w", *addr, err)
	}
	if !isLoopbackHost(host) && !*allowRemote {
		return fmt.Errorf("web: refusing to bind %q; pass --allow-remote to expose the UI beyond this machine", *addr)
	}

	backend := newBackend(cfgPath, cfg)
	defer backend.Close()

	manager, err := jobs.NewManager(*jobsFile, backend.RunJob)
	if err != nil {
		return err
	}
	server, err := web.New(backend, web.Options{
		ReadOnly: *readOnly,
		Version:  report.DefaultVersion,
		Jobs:     manager,
	})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if !*readOnly {
		if err := manager.Start(ctx); err != nil {
			return err
		}
		defer manager.Stop()
	}

	display := net.JoinHostPort(host, port)
	if host == "" || host == "0.0.0.0" || host == "::" {
		display = net.JoinHostPort("localhost", port)
	}
	fmt.Fprintf(stdout, "BeeChief web UI listening on http://%s\n", display)
	if !isLoopbackHost(host) {
		fmt.Fprintf(stdout, "WARNING: the web UI has no authentication; keep this port private.\n")
	}
	return server.ListenAndServe(ctx, *addr)
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
