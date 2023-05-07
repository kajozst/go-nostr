package nostr

import (
	"context"
	"sync"

	syncmap "github.com/SaveTheRbtz/generic-sync-map-go"
)

type SimplePool struct {
	Relays  map[string]*Relay
	Context context.Context

	mutex  sync.Mutex
	cancel context.CancelFunc
}

func NewSimplePool(ctx context.Context) *SimplePool {
	ctx, cancel := context.WithCancel(ctx)

	return &SimplePool{
		Relays: make(map[string]*Relay),

		Context: ctx,
		cancel:  cancel,
	}
}

func (pool *SimplePool) EnsureRelay(url string) *Relay {
	nm := NormalizeURL(url)

	pool.mutex.Lock()
	defer pool.mutex.Unlock()

	relay, ok := pool.Relays[nm]
	if ok && relay.ConnectionContext.Err() == nil {
		// already connected, unlock and return
		return relay
	} else {
		var err error
		// we use this ctx here so when the pool dies everything dies
		relay, err = RelayConnect(pool.Context, nm)
		if err != nil {
			return nil
		}

		pool.Relays[nm] = relay
		return relay
	}
}

// SubMany opens a subscription with the given filters to multiple relays
// the subscriptions only end when the context is canceled
func (pool *SimplePool) SubMany(
	ctx context.Context,
	urls []string,
	filters Filters,
) chan *Event {
	uniqueEvents := make(chan *Event)
	seenAlready := syncmap.MapOf[string, struct{}]{}

	for _, url := range urls {
		go func(nm string) {
			sub, _ := pool.EnsureRelay(nm).Subscribe(ctx, filters)
			if sub == nil {
				return
			}

			for evt := range sub.Events {
				// dispatch unique events to client
				if _, ok := seenAlready.LoadOrStore(evt.ID, struct{}{}); !ok {
					uniqueEvents <- evt
				}
			}
		}(NormalizeURL(url))
	}

	return uniqueEvents
}

// SubManyEose is like SubMany, but it stops subscriptions and closes the channel when gets a EOSE
func (pool *SimplePool) SubManyEose(
	ctx context.Context,
	urls []string,
	filters Filters,
) chan *Event {
	ctx, cancel := context.WithCancel(ctx)

	uniqueEvents := make(chan *Event)
	seenAlready := syncmap.MapOf[string, struct{}]{}
	wg := sync.WaitGroup{}
	wg.Add(len(urls))

	go func() {
		// this will happen when all subscriptions get an eose (or when they die)
		wg.Wait()
		cancel()
		close(uniqueEvents)
	}()

	for _, url := range urls {
		go func(nm string) {
			sub, _ := pool.EnsureRelay(nm).Subscribe(ctx, filters)
			if sub == nil {
				wg.Done()
				return
			}

			defer wg.Done()

			for {
				select {
				case <-sub.EndOfStoredEvents:
					return
				case evt, more := <-sub.Events:
					if !more {
						return
					}

					// dispatch unique events to client
					if _, ok := seenAlready.LoadOrStore(evt.ID, struct{}{}); !ok {
						uniqueEvents <- evt
					}
				}
			}
		}(NormalizeURL(url))
	}

	return uniqueEvents
}