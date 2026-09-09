package realtime

import (
	"encoding/json"
	"sync"

	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
)

type subscription struct {
	studentID string
	role      auth.Role
	events    chan []byte
}

type Hub struct {
	mu            sync.RWMutex
	subscriptions map[*subscription]struct{}
}

func NewHub() *Hub {
	return &Hub{subscriptions: make(map[*subscription]struct{})}
}

func (hub *Hub) Subscribe(studentID string, role auth.Role) (<-chan []byte, func()) {
	subscription := &subscription{studentID: studentID, role: role, events: make(chan []byte, 32)}
	hub.mu.Lock()
	hub.subscriptions[subscription] = struct{}{}
	hub.mu.Unlock()

	return subscription.events, func() {
		hub.mu.Lock()
		if _, ok := hub.subscriptions[subscription]; ok {
			delete(hub.subscriptions, subscription)
			close(subscription.events)
		}
		hub.mu.Unlock()
	}
}

func (hub *Hub) Publish(event Event) error {
	studentBytes, err := json.Marshal(ProjectStudent(event))
	if err != nil {
		return err
	}
	parentBytes, err := json.Marshal(ProjectParent(event))
	if err != nil {
		return err
	}

	hub.mu.RLock()
	defer hub.mu.RUnlock()
	for subscription := range hub.subscriptions {
		if subscription.studentID != event.StudentID {
			continue
		}
		payload := studentBytes
		if subscription.role == auth.RoleParent || subscription.role == auth.RoleOwner {
			if event.ParentSuppressed {
				continue
			}
			payload = parentBytes
		}
		select {
		case subscription.events <- payload:
		default:
			// A slow observer must not block the active classroom.
		}
	}
	return nil
}
