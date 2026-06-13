//go:build !linux && !windows

package helperapp

type noopAutoStarter struct{}

func NewAutoStarter() AutoStarter {
	return noopAutoStarter{}
}

func (noopAutoStarter) IsEnabled() bool {
	return false
}

func (noopAutoStarter) SetEnabled(bool) error {
	return nil
}
