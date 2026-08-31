package subject

import "fmt"

type Code string

const (
	Math      Code = "MATH"
	Chinese   Code = "CHINESE"
	English   Code = "ENGLISH"
	Physics   Code = "PHYSICS"
	Chemistry Code = "CHEMISTRY"
)

var All = []Code{Math, Chinese, English, Physics, Chemistry}

func (code Code) Valid() bool {
	for _, candidate := range All {
		if code == candidate {
			return true
		}
	}
	return false
}

func Parse(value string) (Code, error) {
	code := Code(value)
	if !code.Valid() {
		return "", fmt.Errorf("unsupported subject %q", value)
	}
	return code, nil
}
