package helperapp

type AutoStarter interface {
	IsEnabled() bool
	SetEnabled(bool) error
}
