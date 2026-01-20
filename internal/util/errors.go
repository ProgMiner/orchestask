package util

type wrappedError interface {
	Unwrap() []error
}

func Unwrap(err error) []error {
	if err == nil {
		return nil
	}

	if we, ok := err.(wrappedError); ok {
		return we.Unwrap()
	}

	return []error{err}
}
