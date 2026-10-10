// commit-drop is a one-connection fault injector for the isolated dp-25 lab.
// It forwards PostgreSQL frames, but drops CommandComplete("COMMIT") and closes
// the socket. The server has committed; the client cannot confirm that outcome.
// It is not a database proxy for production, TLS, or general traffic.
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

const maxFrame = 1 << 20

func copyResponses(dst io.Writer, src io.Reader) (bool, error) {
	for {
		var header [5]byte
		if _, err := io.ReadFull(src, header[:]); err != nil {
			return false, err
		}
		n := binary.BigEndian.Uint32(header[1:])
		if n < 4 || n > maxFrame {
			return false, errors.New("invalid or oversized PostgreSQL frame")
		}
		payload := make([]byte, int(n)-4)
		if _, err := io.ReadFull(src, payload); err != nil {
			return false, err
		}
		if header[0] == 'C' && string(payload) == "COMMIT\x00" {
			return true, nil
		}
		if _, err := io.Copy(dst, bytes.NewReader(append(header[:], payload...))); err != nil {
			return false, err
		}
	}
}

func serve(listener net.Listener, target string) error {
	if tcp, ok := listener.(*net.TCPListener); ok {
		if err := tcp.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
			return err
		}
	}
	client, err := listener.Accept()
	if err != nil {
		return err
	}
	defer client.Close()
	server, err := net.DialTimeout("tcp", target, 3*time.Second)
	if err != nil {
		return err
	}
	defer server.Close()
	deadline := time.Now().Add(20 * time.Second)
	if err := client.SetDeadline(deadline); err != nil {
		return err
	}
	if err := server.SetDeadline(deadline); err != nil {
		return err
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = io.Copy(server, client)
	}()
	dropped, copyErr := copyResponses(client, server)
	_ = client.Close()
	_ = server.Close()
	<-finished
	if !dropped {
		return fmt.Errorf("COMMIT response was not dropped: %w", copyErr)
	}
	fmt.Println(`{"event":"commit_command_complete_dropped"}`)
	return nil
}

func main() {
	listen := flag.String("listen", ":6543", "listen address inside the isolated lab network")
	target := flag.String("target", "db:5432", "dp-25 database address")
	flag.Parse()
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot start test listener")
		os.Exit(1)
	}
	defer listener.Close()
	fmt.Println(`{"event":"ready"}`)
	if err := serve(listener, *target); err != nil {
		fmt.Fprintln(os.Stderr, "test proxy failed:", err)
		os.Exit(1)
	}
}
