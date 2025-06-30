package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/whywaita/myshoes/pkg/config"
	"github.com/whywaita/myshoes/pkg/datastore"
	"github.com/whywaita/myshoes/pkg/datastore/mysql"
	"github.com/whywaita/myshoes/pkg/gh"
	"github.com/whywaita/myshoes/pkg/logger"
	"github.com/whywaita/myshoes/pkg/runner"
	"github.com/whywaita/myshoes/pkg/starter"
	"github.com/whywaita/myshoes/pkg/starter/safety/unlimited"
	"github.com/whywaita/myshoes/pkg/web"

	"golang.org/x/sync/errgroup"
)

func init() {
	config.Load()
	mysqlURL := config.LoadMySQLURL()
	config.Config.MySQLDSN = mysqlURL

	if err := gh.InitializeCache(config.Config.GitHub.AppID, config.Config.GitHub.PEMByte); err != nil {
		log.Panicf("failed to create a cache: %+v", err)
	}
}

func main() {
	runtime.SetBlockProfileRate(1)
	runtime.SetMutexProfileFraction(1)
	go func() {
		log.Fatal(http.ListenAndServe("localhost:6060", nil))
	}()

	myshoes, err := newShoes()
	if err != nil {
		log.Fatalln(err)
	}

	if err := myshoes.Run(); err != nil {
		log.Fatalln(err)
	}
}

type myShoes struct {
	ds    datastore.Datastore
	start *starter.Starter
	run   *runner.Manager
}

// newShoes create myshoes.
func newShoes() (*myShoes, error) {
	notifyEnqueueCh := make(chan struct{}, 1)

	ds, err := mysql.New(config.Config.MySQLDSN, notifyEnqueueCh)
	if err != nil {
		return nil, fmt.Errorf("failed to mysql.New: %w", err)
	}

	unlimit := unlimited.Unlimited{}
	s := starter.New(ds, unlimit, config.Config.RunnerVersion, config.Config.EnableRescueWorkflow, notifyEnqueueCh)

	manager := runner.New(ds, config.Config.RunnerVersion)

	return &myShoes{
		ds:    ds,
		start: s,
		run:   manager,
	}, nil
}

// Run start services.
func (m *myShoes) Run() error {
	eg, ctx := errgroup.WithContext(context.Background())

	for {
		logger.Logf(false, "start getting lock...")
		isLocked, err := m.ds.IsLocked(ctx)
		if err != nil {
			return fmt.Errorf("failed to check lock: %w", err)
		}

		if strings.EqualFold(isLocked, datastore.IsNotLocked) {
			if err := m.ds.GetLock(ctx); err != nil {
				return fmt.Errorf("failed to get lock: %w", err)
			}

			logger.Logf(false, "get lock successfully!")
			break
		}

		time.Sleep(time.Second)
	}

	eg.Go(func() error {
		if err := web.Serve(ctx, m.ds); err != nil {
			logger.Logf(false, "failed to web.Serve: %+v", err)
			return fmt.Errorf("failed to serve: %w", err)
		}
		return nil
	})
	eg.Go(func() error {
		if err := m.start.Loop(ctx); err != nil {
			logger.Logf(false, "failed to starter manager: %+v", err)
			return fmt.Errorf("failed to starter loop: %w", err)
		}
		return nil
	})
	eg.Go(func() error {
		if err := m.run.Loop(ctx); err != nil {
			logger.Logf(false, "failed to runner manager: %+v", err)
			return fmt.Errorf("failed to runner loop: %w", err)
		}
		return nil
	})

	if err := eg.Wait(); err != nil {
		return fmt.Errorf("failed to wait errgroup: %w", err)
	}

	return nil
}
