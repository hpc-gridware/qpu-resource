package availability

import "context"

type AvailabilityStatus struct {
	Ready          bool
	Reason         string
	SlotsAvailable *int
}

type AvailabilityProvider interface {
	Status(context.Context) (AvailabilityStatus, error)
}
