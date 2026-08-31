package contentpipeline

import (
	"context"
	"errors"
	"strings"
)

type SecondaryReviewer interface {
	Review(ctx context.Context, asset Asset) (Review, ReviewEvidence, error)
}

type ReviewEvidence struct {
	Provider  string
	Model     string
	RequestID string
}

type ReviewService struct {
	generatorIdentity string
	reviewerIdentity  string
	reviewer          SecondaryReviewer
}

func NewReviewService(generatorIdentity, reviewerIdentity string, reviewer SecondaryReviewer) (*ReviewService, error) {
	if reviewer == nil {
		return nil, errors.New("secondary reviewer is required")
	}
	if generatorIdentity == "" || reviewerIdentity == "" || generatorIdentity == reviewerIdentity {
		return nil, errors.New("secondary reviewer must be independent from generator")
	}
	return &ReviewService{generatorIdentity: generatorIdentity, reviewerIdentity: reviewerIdentity, reviewer: reviewer}, nil
}

func (service *ReviewService) Review(ctx context.Context, asset Asset) (Review, error) {
	review, _, err := service.reviewer.Review(ctx, asset)
	return review, err
}

func (service *ReviewService) ReviewWithEvidence(ctx context.Context, asset Asset) (Review, ReviewEvidence, error) {
	review, evidence, err := service.reviewer.Review(ctx, asset)
	if err != nil {
		return Review{}, ReviewEvidence{}, err
	}
	if evidence.Provider+":"+evidence.Model != service.reviewerIdentity || strings.TrimSpace(evidence.RequestID) == "" {
		return Review{}, ReviewEvidence{}, errors.New("secondary reviewer returned invalid provenance")
	}
	return review, evidence, nil
}

func (service *ReviewService) Identity() string { return service.reviewerIdentity }
