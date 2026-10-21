package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/axmishell/mikvacepanel/internal/client"
	"github.com/axmishell/mikvacepanel/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch os.Args[1] {
	case "info":
		err = cmdInfo(ctx, os.Args[2:])
	case "sub":
		err = cmdSub(ctx, os.Args[2:])
	case "check":
		err = cmdCheck(ctx, os.Args[2:])
	case "run":
		err = cmdRun(ctx, os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println("mikvace-client", version.String(), version.Platform())
		return
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `MikvAce client

Usage:
  mikvace-client info  --url <subscription-url>
  mikvace-client sub   --url <subscription-url> [--format clash|singbox|uri|xray-json] [--out file]
  mikvace-client check --url <subscription-url> [--tcp]
  mikvace-client run   --url <subscription-url> [--node N] [--socks5 127.0.0.1:1080]
                       [--http 127.0.0.1:8080] [--hysteria hysteria]
  mikvace-client version
`)
}

func cmdInfo(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	subURL := fs.String("url", "", "subscription URL")
	_ = fs.Parse(args)
	if *subURL == "" {
		return fmt.Errorf("--url is required")
	}
	info, err := client.NewHTTPClient().FetchInfo(ctx, *subURL)
	if err != nil {
		return err
	}
	limit := "unlimited"
	if info.Traffic.Limit > 0 {
		limit = client.FormatBytes(info.Traffic.Limit)
	}
	expire := "never"
	if info.Expire != "" && !strings.HasPrefix(info.Expire, "0001-") {
		expire = info.Expire
	}
	fmt.Printf("User:      %s (%s)\n", firstNonEmpty(info.Username, info.UserID), info.UserID)
	fmt.Printf("Enabled:   %v\n", info.Enabled)
	fmt.Printf("Traffic:   %s / %s\n", client.FormatBytes(info.Traffic.Used), limit)
	fmt.Printf("Expires:   %s\n", expire)
	fmt.Printf("Servers:   %d\n", info.Servers)
	return nil
}

func cmdSub(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sub", flag.ExitOnError)
	subURL := fs.String("url", "", "subscription URL")
	format := fs.String("format", "", "clash, singbox, uri or xray-json")
	out := fs.String("out", "", "output file (default stdout)")
	_ = fs.Parse(args)
	if *subURL == "" {
		return fmt.Errorf("--url is required")
	}
	body, headers, err := client.NewHTTPClient().Fetch(ctx, *subURL, *format)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "format: %s  title: %s  traffic: %s\n",
		headers.Get("X-Subscription-Format"), decodeTitle(headers.Get("Profile-Title")),
		headers.Get("Subscription-Userinfo"))
	if *out == "" {
		_, err = os.Stdout.Write(body)
		return err
	}
	if err := os.WriteFile(*out, body, 0o600); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "saved %d bytes to %s\n", len(body), *out)
	return nil
}

func cmdCheck(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	subURL := fs.String("url", "", "subscription URL")
	tcp := fs.Bool("tcp", false, "also try a TCP connection to the node port (Hysteria itself uses UDP)")
	_ = fs.Parse(args)
	if *subURL == "" {
		return fmt.Errorf("--url is required")
	}
	body, _, err := client.NewHTTPClient().Fetch(ctx, *subURL, "uri")
	if err != nil {
		return err
	}
	endpoints, err := client.ParseURIList(body)
	if err != nil {
		return err
	}
	results := client.CheckEndpoints(ctx, endpoints, *tcp)
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "#\tNAME\tADDRESS\tHOST\tPORT\tDNS\tSTATUS")
	for i, r := range results {
		status := "ok"
		if r.Error != "" {
			status = r.Error
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%d\t%dms\t%s\n", i+1, r.Name, r.Address, r.Host, r.Port, r.Millis, status)
	}
	_ = w.Flush()
	return nil
}

func cmdRun(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	subURL := fs.String("url", "", "subscription URL")
	node := fs.Int("node", 1, "1-based endpoint index (see check)")
	socks5 := fs.String("socks5", "127.0.0.1:1080", "local SOCKS5 listen address (empty to disable)")
	httpAddr := fs.String("http", "", "local HTTP proxy listen address")
	bin := fs.String("hysteria", "hysteria", "path to the Hysteria binary")
	_ = fs.Parse(args)
	if *subURL == "" {
		return fmt.Errorf("--url is required")
	}
	body, _, err := client.NewHTTPClient().Fetch(ctx, *subURL, "uri")
	if err != nil {
		return err
	}
	endpoints, err := client.ParseURIList(body)
	if err != nil {
		return err
	}
	if *node < 1 || *node > len(endpoints) {
		return fmt.Errorf("--node must be between 1 and %d", len(endpoints))
	}
	ep := endpoints[*node-1]
	fmt.Fprintf(os.Stderr, "connecting to %s (%s:%d)\n", ep.Name, ep.Host, ep.Port)
	if ep.PortRange != "" {
		fmt.Fprintf(os.Stderr, "port hopping range: %s\n", ep.PortRange)
	}
	fmt.Fprintf(os.Stderr, "SOCKS5: %s  HTTP: %s\n", firstNonEmpty(*socks5, "off"), firstNonEmpty(*httpAddr, "off"))
	return client.RunHysteria(ctx, *bin, ep, *socks5, *httpAddr)
}

func decodeTitle(v string) string {
	if strings.HasPrefix(v, "base64:") {
		if b, err := base64Decode(v[7:]); err == nil {
			return string(b)
		}
	}
	return v
}

func base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
