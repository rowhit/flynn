package discover

import (
	"github.com/coreos/go-etcd/etcd"
	"github.com/coreos/etcd/store"
	"fmt"
	"strings"
)

type EtcdBackend struct {
	Client *etcd.Client
}

func (b *EtcdBackend) Subscribe(name string) (ch chan *ServiceUpdate, err error) {
	ch = make(chan *ServiceUpdate, 10)
	responses, _ := b.getCurrentState(name)
	for _, response := range responses {
		update := b.responseToUpdate(response)
		if update != nil {
			ch <- update
		}
	}
	go func(watch chan *store.Response) {
		for {
			update := b.responseToUpdate(<-watch)
			if update != nil {
				ch <- update
			}
		}
	}(b.getStateChanges(name))
	return
}

func (b *EtcdBackend) responseToUpdate(resp *store.Response) *ServiceUpdate {
	serviceName := strings.SplitN(resp.Key, "/", 4)[2]
	switch {
		case ("SET" == resp.Action && resp.NewKey) || "GET" == resp.Action:
			return &ServiceUpdate{
				Name:	serviceName,
				Addr:	resp.Value,
				Online:	true,
				Attrs:	map[string]string{},
			}
		case "DELETE" == resp.Action:
			return &ServiceUpdate{
				Name:	serviceName,
				Addr:	resp.PrevValue,
				Online:	false,
				Attrs:	map[string]string{},
			}
	}
	return nil
}

func (b *EtcdBackend) getCurrentState(name string) ([]*store.Response, error) {
	return b.Client.Get(fmt.Sprintf("/services/%s", name))
}

func (b *EtcdBackend) getStateChanges(name string) (chan *store.Response) {
	watch := make(chan *store.Response, 10)
	go b.Client.Watch(fmt.Sprintf("/services/%s", name), 0, watch, nil)
	return watch
}

func (b *EtcdBackend) Register(name string, addr string, attrs map[string]string) error {
	_, err := b.Client.Set(fmt.Sprintf("/services/%s/%s", name, addr), addr, HeartbeatIntervalSecs + MissedHearbeatTTL)
	if err != nil {
		return err
	}
	return nil
}

func (b *EtcdBackend) Unregister(name string, addr string) error {
	_, err := b.Client.Delete(fmt.Sprintf("/services/%s/%s", name, addr))
	if err != nil {
		return err
	}
	return nil
}

func (b *EtcdBackend) Heartbeat(name string, addr string) error {
	// Heartbeat currently just calls Register because eventually Register will also update attributes
	// where Heartbeat will not
	return b.Register(name, addr, map[string]string{})
}