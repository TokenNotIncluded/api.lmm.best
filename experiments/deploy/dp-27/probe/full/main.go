// Health-check candidate. It does not contain or import the user's CLI.
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

var revision = "dp27-probe-v1"

func check(ctx context.Context, port string) error {
	if port != "8080" && port != "8081" {
		return fmt.Errorf("port must be 8080 or 8081")
	}
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            (&net.Dialer{Timeout: time.Second}).DialContext,
		DisableKeepAlives:      true,
		MaxResponseHeaderBytes: 8192,
		ResponseHeaderTimeout:  time.Second,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport:     transport,
		Timeout:       2 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:"+port+"/health/live", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health status %d", response.StatusCode)
	}
	return nil
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
