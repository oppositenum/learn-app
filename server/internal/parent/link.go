package parent

import (
	"time"

	"github.com/google/uuid"
)

type LinkStatus string

const (
	LinkActive  LinkStatus = "ACTIVE"
	LinkRevoked LinkStatus = "REVOKED"
)

type StudentLink struct {
	ParentUserID uuid.UUID
	StudentID    uuid.UUID
	Status       LinkStatus
	CreatedAt    time.Time
	RevokedAt    *time.Time
}

func (link StudentLink) Active() bool {
	return link.Status == LinkActive && link.RevokedAt == nil
}
