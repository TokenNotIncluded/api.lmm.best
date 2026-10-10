package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/credittransition"
	"github.com/LIghtJUNction/api.lmm.best/model"
)

func runCreditPreparation(mode string, args []string) error {
	if _, err := os.Lstat(".env"); !errors.Is(err, os.ErrNotExist) {
		return errors.New("credit preparation requires a working directory without .env")
	}
	config, err := credittransition.ReadConfig(os.Getenv(credittransition.PlanEnvironment), os.Getenv(credittransition.DigestEnvironment))
	if err != nil {
		return err
	}
	provider, err := os.Executable()
	if err != nil {
		return err
	}
	if err := config.VerifyProvider(provider); err != nil {
		return err
	}
	flags := flag.NewFlagSet("credit preparation", flag.ContinueOnError)
	port := flags.Int("port", 3000, "loopback preparation port")
	_ = flags.String("log-dir", "", "accepted for service command compatibility; preparation logs go to the journal")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected preparation arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db, err := model.OpenCreditPreparationDB(ctx, config)
	if err != nil {
		return err
	}
	pool, err := db.DB()
	if err != nil {
		return err
	}
	defer pool.Close()
	if mode == "apply" {
		if err := model.ApplyCreditPreparationSchema(ctx, db, config); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(os.Stdout, "credit transition schema preparation completed; no financial values changed")
		return nil
	}
	if err := model.VerifyCreditPreparation(ctx, db, config); err != nil {
		return err
	}
	if mode == "verify" {
		_, _ = fmt.Fprintln(os.Stdout, "credit transition schema preparation verified; business remains disabled")
		return nil
	}
	if mode != "serve" {
		return errors.New("invalid credit preparation mode")
	}
	address, err := creditPreparationListenAddress(os.Getenv("LMM_API_BIND_ADDRESS"), resolvePort(os.Getenv("PORT"), os.Getenv("LMM_API_PORT"), *port))
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: credittransition.Handler(config, os.Getenv(credittransition.DigestEnvironment), common.Version,
		func(ctx context.Context) error { return model.VerifyCreditPreparation(ctx, db, config) }),
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 15 * time.Second}
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	_, _ = fmt.Fprintf(os.Stdout, "credit_transition_prepare ready=true business_enabled=false transition_id=%s config_sha256=%s\n", config.TransitionID, os.Getenv(credittransition.DigestEnvironment))
	select {
	case err := <-served:
		return err
	case sig := <-quit:
		_, _ = fmt.Fprintf(os.Stdout, "received signal: %v, shutting down credit preparation...\n", sig)
	}
	shutdown, finish := context.WithTimeout(context.Background(), 15*time.Second)
	defer finish()
	if err := server.Shutdown(shutdown); err != nil {
		return errors.Join(err, server.Close())
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	if err := pool.Close(); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(os.Stdout, "credit_transition_prepare shutdown_complete=true business_enabled=false")
	_, _ = fmt.Fprintln(os.Stdout, "server exited")
	return nil
}

func creditPreparationListenAddress(bind, port string) (string, error) {
	address, err := buildListenAddress(bind, port)
	if err != nil {
		return "", err
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", err
	}
	parsed, err := netip.ParseAddr(host)
	if err != nil || !parsed.IsLoopback() {
		return "", errors.New("credit preparation listener must remain on loopback")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return "", errors.New("invalid credit preparation port")
	}
	return address, nil
}
