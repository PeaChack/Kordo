//go:build windows

// portico-svc is the privileged Portico service.
//
//	portico-svc install    install and start the Windows service (admin)
//	portico-svc uninstall  stop and remove the service, remove firewall filters
//	portico-svc run        run in the console (admin, for debugging)
//	portico-svc start|stop control the installed service
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/portico-vpn/portico/windows/killswitch"
	"github.com/portico-vpn/portico/windows/service"
)

const (
	serviceName = "PorticoSvc"
	displayName = "Portico VPN Service"
	description = "Per-application VPN routing (Portico). Runs the network engine, kill switch and process tracker."
)

func main() {
	isService, err := svc.IsWindowsService()
	if err != nil {
		fatal(err)
	}
	if isService {
		if err := svc.Run(serviceName, &handler{}); err != nil {
			fatal(err)
		}
		return
	}
	cmd := "help"
	if len(os.Args) > 1 {
		cmd = strings.ToLower(os.Args[1])
	}
	switch cmd {
	case "run":
		runConsole()
	case "install":
		check(install())
	case "uninstall":
		check(uninstall())
	case "start":
		check(control(true))
	case "stop":
		check(control(false))
	default:
		fmt.Println("usage: portico-svc install | uninstall | run | start | stop")
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func check(err error) {
	if err != nil {
		fatal(err)
	}
	fmt.Println("ok")
}

func runConsole() {
	s, err := service.New(service.DefaultDataDir(), true)
	if err != nil {
		fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := s.Run(ctx); err != nil {
		fatal(err)
	}
}

type handler struct{}

const pbtAPMResumeAutomatic = 0x12

func (h *handler) Execute(args []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	s, err := service.New(service.DefaultDataDir(), false)
	if err != nil {
		return true, 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPowerEvent}
	for {
		select {
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-done:
				case <-time.After(20 * time.Second):
				}
				return false, 0
			case svc.PowerEvent:
				if c.EventType == pbtAPMResumeAutomatic {
					s.Resume()
				}
			}
		case err := <-done:
			cancel()
			if err != nil {
				return true, 2
			}
			return false, 0
		}
	}
}

func install() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager (run as administrator): %w", err)
	}
	defer m.Disconnect()
	if s, err := m.OpenService(serviceName); err == nil {
		s.Close()
		return fmt.Errorf("service %s already installed", serviceName)
	}
	s, err := m.CreateService(serviceName, exe, mgr.Config{
		DisplayName:      displayName,
		Description:      description,
		StartType:        mgr.StartAutomatic,
		DelayedAutoStart: true,
		ServiceStartName: "LocalSystem",
	})
	if err != nil {
		return err
	}
	defer s.Close()
	_ = s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 2 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
	}, 3600)
	return s.Start()
}

func uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager (run as administrator): %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err == nil {
		_, _ = s.Control(svc.Stop)
		for i := 0; i < 40; i++ {
			st, err := s.Query()
			if err != nil || st.State == svc.Stopped {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		if err := s.Delete(); err != nil {
			s.Close()
			return err
		}
		s.Close()
	}
	// leave no filters behind
	if fw, err := killswitch.Open(nil); err == nil {
		_ = fw.Clear()
		fw.Close()
	}
	return nil
}

func control(start bool) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return err
	}
	defer s.Close()
	if start {
		return s.Start()
	}
	_, err = s.Control(svc.Stop)
	return err
}
