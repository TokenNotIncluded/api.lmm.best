// Health-check candidate. It does not contain or import the user's CLI.
package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

var revision = "dp27-probe-v1"

func check(ctx context.Context, port string) error {
	if port != "8080" && port != "8081" {
		return fmt.Errorf("port must be 8080 or 8081")
	}
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", "127.0.0.1:"+port)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(conn, "GET /health/live HTTP/1.1\r\nHost: 127.0.0.1:%s\r\nConnection: close\r\n\r\n", port); err != nil {
		return err
	}
	// Read a bounded status line and complete headers. Do not follow redirects, read a
	// response body, use proxies, resolve remote names, or import the user CLI.
	reader := bufio.NewReaderSize(conn, 1024)
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return err
	}
	if !strings.HasSuffix(string(line), "\r\n") {
		return fmt.Errorf("invalid HTTP status line")
	}
	fields := strings.SplitN(strings.TrimSuffix(string(line), "\r\n"), " ", 3)
	if len(fields) != 3 || (fields[0] != "HTTP/1.1" && fields[0] != "HTTP/1.0") || fields[1] != "200" {
		return fmt.Errorf("health status is not HTTP 200")
	}

	total := len(line)
	for {
		line, err = reader.ReadSlice('\n')
		if err != nil {
			return err
		}
		total += len(line)
		if total > 8192 || !strings.HasSuffix(string(line), "\r\n") {
			return fmt.Errorf("invalid or oversized HTTP headers")
		}
		if string(line) == "\r\n" {
			return nil
		}
		colon := strings.IndexByte(string(line), ':')
		if colon < 1 {
			return fmt.Errorf("invalid HTTP header")
		}
	}
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(revision)
		return
	}
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: lmm-healthprobe 8080|8081|--version")
		os.Exit(2)
	}
	if err := check(context.Background(), os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
