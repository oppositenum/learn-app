package contentpipeline

import "errors"

var ErrReleaseGate = errors.New("content release gate rejected transition")

type Gate struct{}

func (Gate) AfterValidation(current Status, validation Validation) (Status, error) {
	if current != Draft {
		return current, ErrReleaseGate
	}
	if !validation.Passed {
		return RejectedAutomatic, nil
	}
	return AutomaticValidated, nil
}

func (Gate) AfterReview(current Status, validation Validation, review Review, assetSchema, validationSchema, reviewSchema string) (Status, error) {
	if current != AutomaticValidated || !validation.Passed {
		return current, ErrReleaseGate
	}
	if assetSchema != CurrentSchemaVersion || validationSchema != assetSchema || reviewSchema != assetSchema {
		return current, ErrReleaseGate
	}
	if review.Result != ReviewPass || !review.AgeAppropriate || !review.FactuallySound || !review.Unambiguous || !review.NoAnswerLeak || !review.SafeValues {
		return current, ErrReleaseGate
	}
	return AIReviewed, nil
}

func (Gate) Release(current Status, validation Validation, review Review) (Status, error) {
	if current != AIReviewed || !validation.Passed || review.Result != ReviewPass {
		return current, ErrReleaseGate
	}
	return Released, nil
}

func (Gate) Quarantine(current Status, reason string) (Status, error) {
	if current != Released || reason == "" {
		return current, ErrReleaseGate
	}
	return Quarantined, nil
}
