package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

const (
	configFileName        = "white-ip.conf"
	reconnectDownDuration = 3 * time.Second
	reconnectWaitTimeout  = 30 * time.Second
	windowPollInterval    = 30 * time.Second
	configErrorRetryDelay = 30 * time.Second
)

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

type Guard struct {
	router  *RCIClient
	checker *IPChecker
}

func NewGuard(router *RCIClient, checker *IPChecker) *Guard {
	return &Guard{router: router, checker: checker}
}

func (g *Guard) resolveWAN(ctx context.Context, cfg Config) (string, error) {
	if cfg.WANInterface != "" {
		return cfg.WANInterface, nil
	}
	return g.router.WANInterfaceID(ctx)
}

func (g *Guard) checkOnce(ctx context.Context, cfg Config) (wanID string, white bool, err error) {
	wanID, err = g.resolveWAN(ctx, cfg)
	if err != nil {
		return "", false, err
	}
	wanIP, up, err := g.router.InterfaceState(ctx, wanID)
	if err != nil {
		return wanID, false, err
	}
	if !up || wanIP == nil {
		return wanID, false, nil
	}
	extIP, err := g.checker.External(ctx, cfg.CheckURLs)
	if err != nil {
		return wanID, false, err
	}
	log.Printf("wan=%s external=%s", wanIP, extIP)
	return wanID, wanIP.Equal(extIP), nil
}

func (g *Guard) reconnect(ctx context.Context, wanID string) error {
	log.Printf("grey IP detected, reconnecting interface %s", wanID)
	if err := g.router.SetInterfaceUp(ctx, wanID, false); err != nil {
		return err
	}
	if !sleepCtx(ctx, reconnectDownDuration) {
		return ctx.Err()
	}
	if err := g.router.SetInterfaceUp(ctx, wanID, true); err != nil {
		return err
	}
	_, err := g.router.WaitForAddress(ctx, wanID, reconnectWaitTimeout)
	return err
}

func (g *Guard) Run(ctx context.Context, configPath string) {
	attempts := 0
	for ctx.Err() == nil {
		cfg, err := LoadConfig(configPath)
		if err != nil {
			log.Printf("config: %v", err)
			sleepCtx(ctx, configErrorRetryDelay)
			continue
		}

		now := cfg.Now()
		if !cfg.Window.Contains(now) {
			wait := min(time.Until(cfg.Window.NextStart(now)), windowPollInterval)
			sleepCtx(ctx, wait)
			continue
		}

		wanID, white, err := g.checkOnce(ctx, cfg)
		if err != nil {
			log.Printf("check failed: %v", err)
		} else if white {
			log.Printf("white IP confirmed")
			attempts = 0
			sleepCtx(ctx, cfg.Interval)
			continue
		}

		attempts++
		log.Printf("attempt %d/%d", attempts, cfg.MaxAttempts)
		if attempts > cfg.MaxAttempts {
			log.Printf("attempt limit reached, waiting for next active period")
			sleepCtx(ctx, time.Until(cfg.Window.NextStart(cfg.Now())))
			attempts = 0
			continue
		}

		if wanID != "" {
			if err := g.reconnect(ctx, wanID); err != nil {
				log.Printf("reconnect failed: %v", err)
			}
		}
		sleepCtx(ctx, cfg.Interval)
	}
}

const testTimeFormat = "2006-01-02 15:04:05 MST"

func printTestConfig(cfg Config, cfgErr error) {
	fmt.Printf("Системное время: %s\n", time.Now().Format(testTimeFormat))
	if cfgErr != nil {
		fmt.Printf("Местное время: %s (смещение не задано, конфигурация таймера не загружена: %v)\n", time.Now().Format(testTimeFormat), cfgErr)
		return
	}
	fmt.Printf("Местное время: %s\n", cfg.Now().Format(testTimeFormat))
	fmt.Println("Параметры таймера из конфигурации:")
	fmt.Printf("  активный период: %s - %s\n", cfg.Window.From, cfg.Window.To)
	fmt.Printf("  интервал между попытками: %s\n", cfg.Interval)
	fmt.Printf("  макс. число попыток подряд: %d\n", cfg.MaxAttempts)
	if cfg.UTCOffset != nil {
		fmt.Printf("  смещение местного времени: UTC%+d\n", *cfg.UTCOffset)
	} else {
		fmt.Printf("  смещение местного времени: не задано (используется системное время)\n")
	}
}

func runTest(ctx context.Context, configPath string) int {
	cfg, cfgErr := LoadConfig(configPath)
	printTestConfig(cfg, cfgErr)

	wanInterface, checkURLs := cfg.WANInterface, cfg.CheckURLs
	if cfgErr != nil {
		wanInterface, checkURLs = LoadRuntimeOverrides(configPath)
	}

	router := NewRCIClient(rciBaseURL)
	checker := NewIPChecker()

	wanID := wanInterface
	if wanID == "" {
		id, err := router.WANInterfaceID(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "не удалось определить WAN-интерфейс: %v\n", err)
			return 1
		}
		wanID = id
	}
	fmt.Printf("WAN-интерфейс: %s\n", wanID)

	wanIP, up, err := router.InterfaceState(ctx, wanID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "не удалось получить состояние интерфейса: %v\n", err)
		return 1
	}
	if !up || wanIP == nil {
		fmt.Printf("IP на WAN: не определён (интерфейс не в состоянии up)\n")
		fmt.Printf("Результат: серый IP\n")
		return 0
	}
	fmt.Printf("IP на WAN: %s\n", wanIP)

	extIP, err := checker.External(ctx, checkURLs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "не удалось получить внешний IP: %v\n", err)
		return 1
	}
	fmt.Printf("IP на внешнем ресурсе: %s\n", extIP)

	if wanIP.Equal(extIP) {
		fmt.Println("Результат: белый IP")
	} else {
		fmt.Println("Результат: серый IP")
	}
	return 0
}

func main() {
	log.SetFlags(log.LstdFlags)

	testMode := flag.Bool("test", false, "разовая проверка WAN/внешнего IP с выводом результата, без запуска демона")
	flag.Parse()

	exePath, err := os.Executable()
	if err != nil {
		log.Fatalf("resolve executable path: %v", err)
	}
	configPath := filepath.Join(filepath.Dir(exePath), configFileName)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *testMode {
		os.Exit(runTest(ctx, configPath))
	}

	guard := NewGuard(NewRCIClient(rciBaseURL), NewIPChecker())
	guard.Run(ctx, configPath)
}
