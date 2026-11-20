package desktop

import (
	"context"
	"log"
	"sync"
	"time"
)

type Observer struct {
	Name   string
	Select func(context.Context) (string, error)
	Run    func(context.Context, string) error
}

func RunObservers(ctx context.Context, changes func() <-chan struct{}, observers ...Observer) {
	var workers sync.WaitGroup
	for _, observer := range observers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			runObserver(ctx, changes, observer)
		}()
	}
	workers.Wait()
}

func runObserver(ctx context.Context, changes func() <-chan struct{}, observer Observer) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	var key string
	var stop context.CancelFunc
	var done <-chan error
	cancelAndWait := func() {
		if stop != nil {
			stop()
			<-done
			stop, done, key = nil, nil, ""
		}
	}
	defer cancelAndWait()
	for ctx.Err() == nil {
		changed := changes()
		next, err := observer.Select(ctx)
		if err != nil {
			log.Printf("%s observer configuration: %v", observer.Name, err)
			next = ""
		}
		if next != key {
			cancelAndWait()
			if next != "" && ctx.Err() == nil {
				child, cancel := context.WithCancel(ctx)
				result := make(chan error, 1)
				key, stop, done = next, cancel, result
				go func() { result <- observer.Run(child, next) }()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-changed:
		case <-ticker.C:
		case err := <-done:
			if err != nil && ctx.Err() == nil {
				log.Printf("%s observer: %v", observer.Name, err)
			}
			stop()
			stop, done, key = nil, nil, ""
			select {
			case <-ctx.Done():
				return
			case <-changed:
			case <-ticker.C:
			}
		}
	}
}
