package contentpipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

var ErrReviewerUnavailable = errors.New("independent content reviewer is unavailable")

type Service struct {
	repository *Repository
	validator  Validator
	reviewer   *ReviewService
	generator  ContentGenerator
}

func NewService(repository *Repository, validator Validator, reviewer *ReviewService) *Service {
	return &Service{repository: repository, validator: validator, reviewer: reviewer}
}

func (service *Service) WithGenerator(generator ContentGenerator) *Service {
	service.generator = generator
	return service
}

func (service *Service) GenerationOptions(ctx context.Context) (GenerationOptions, error) {
	if service == nil || service.repository == nil {
		return GenerationOptions{}, errors.New("content repository is required")
	}
	knowledgePoints, sources, err := service.repository.GenerationOptions(ctx)
	if err != nil {
		return GenerationOptions{}, err
	}
	return GenerationOptions{GeneratorAvailable: service.generator != nil, KnowledgePoints: knowledgePoints, Sources: sources}, nil
}

func (service *Service) GenerateDrafts(ctx context.Context, request GenerateRequest) ([]Asset, error) {
	if service == nil || service.repository == nil {
		return nil, errors.New("content repository is required")
	}
	if service.generator == nil {
		return nil, ErrGeneratorUnavailable
	}
	if err := validateGenerateRequest(request); err != nil {
		return nil, err
	}
	input, err := service.repository.GenerationContext(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("load generation context: %w", err)
	}
	generated, evidence, err := service.generator.Generate(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("generate content drafts: %w", err)
	}
	if len(generated.Questions) != request.Count {
		return nil, fmt.Errorf("generated question count %d does not match requested count %d", len(generated.Questions), request.Count)
	}
	assets := make([]Asset, 0, len(generated.Questions))
	seen := make(map[string]bool, len(generated.Questions))
	for index, question := range generated.Questions {
		asset, err := generatedAsset(input, request.SourceID, question, index)
		if err != nil {
			return nil, err
		}
		validation, err := service.validateAsset(ctx, asset)
		if err != nil {
			return nil, err
		}
		if !validation.Passed {
			return nil, fmt.Errorf("generated question %d failed local preflight: %v", index+1, validation.Checks)
		}
		hash := NormalizedPromptHash(asset.PromptPublic)
		if seen[hash] {
			return nil, fmt.Errorf("generated question %d duplicates another generated prompt", index+1)
		}
		duplicate, err := service.repository.DuplicateExists(ctx, hash, uuid.Nil)
		if err != nil {
			return nil, err
		}
		if duplicate {
			return nil, fmt.Errorf("generated question %d duplicates existing content", index+1)
		}
		seen[hash] = true
		assets = append(assets, asset)
	}
	if strings.TrimSpace(evidence.Provider) == "" || strings.TrimSpace(evidence.Model) == "" || strings.TrimSpace(evidence.RequestID) == "" {
		return nil, errors.New("generator returned invalid provenance")
	}
	if err := service.repository.CreateDrafts(ctx, assets, evidence); err != nil {
		return nil, err
	}
	return assets, nil
}

func (service *Service) ImportDraft(ctx context.Context, asset Asset, generation GenerationMetadata) error {
	if service == nil || service.repository == nil {
		return errors.New("content repository is required")
	}
	if strings.TrimSpace(generation.Provider) == "" || strings.TrimSpace(generation.Model) == "" {
		return errors.New("generator provider and model are required")
	}
	validation, err := service.validateAsset(ctx, asset)
	if err != nil {
		return err
	}
	if !checkPassed(validation, "schema") {
		return errors.New("draft asset does not satisfy the content schema")
	}
	return service.repository.CreateDraft(ctx, asset, generation)
}

func (service *Service) validateAsset(ctx context.Context, asset Asset) (Validation, error) {
	knowledgePointID, err := uuid.Parse(asset.KnowledgePointID)
	if err != nil {
		return Validation{}, fmt.Errorf("invalid knowledge point: %w", err)
	}
	allowed, err := service.repository.AllowedMisconceptionCodes(ctx, knowledgePointID)
	if err != nil {
		return Validation{}, fmt.Errorf("load misconception taxonomy: %w", err)
	}
	return service.validator.ValidateWithMisconceptionTaxonomy(asset, allowed), nil
}

func (service *Service) ReviseReleasedDraft(ctx context.Context, asset Asset, actorID uuid.UUID, reason string, generation GenerationMetadata) error {
	if service == nil || service.repository == nil {
		return errors.New("content repository is required")
	}
	validation, err := service.validateAsset(ctx, asset)
	if err != nil {
		return err
	}
	if !checkPassed(validation, "schema") {
		return errors.New("revised draft asset does not satisfy the content schema")
	}
	return service.repository.ReviseReleasedDraft(ctx, asset, actorID, reason, generation)
}

func (service *Service) Validate(ctx context.Context, questionID uuid.UUID) (uuid.UUID, Status, Validation, error) {
	asset, _, err := service.repository.LoadAsset(ctx, questionID)
	if err != nil {
		return uuid.Nil, "", Validation{}, err
	}
	validation, err := service.validateAsset(ctx, asset)
	if err != nil {
		return uuid.Nil, "", Validation{}, err
	}
	duplicate, err := service.repository.DuplicateExists(ctx, NormalizedPromptHash(asset.PromptPublic), questionID)
	if err != nil {
		return uuid.Nil, "", Validation{}, err
	}
	validation = replaceCheck(validation, check("duplicate", !duplicate, "normalized prompt must not duplicate another asset"))
	id, status, err := service.repository.RecordValidation(ctx, questionID, asset.ContentVersion, asset.SchemaVersion, validation)
	return id, status, validation, err
}

func (service *Service) Review(ctx context.Context, questionID uuid.UUID) (uuid.UUID, Status, Review, error) {
	if service.reviewer == nil {
		return uuid.Nil, "", Review{}, ErrReviewerUnavailable
	}
	asset, generation, err := service.repository.LoadAsset(ctx, questionID)
	if err != nil {
		return uuid.Nil, "", Review{}, err
	}
	if generation.Provider+":"+generation.Model == service.reviewer.Identity() {
		return uuid.Nil, "", Review{}, errors.New("secondary reviewer must differ from generator")
	}
	review, evidence, err := service.reviewer.ReviewWithEvidence(ctx, asset)
	if err != nil {
		return uuid.Nil, "", Review{}, err
	}
	id, status, err := service.repository.RecordReview(ctx, questionID, asset.ContentVersion, asset.SchemaVersion, evidence.Provider, evidence.Model, evidence.RequestID, review)
	return id, status, review, err
}

func (service *Service) Release(ctx context.Context, questionID, actorID uuid.UUID, reason string) error {
	validationID, reviewID, err := service.repository.ReleaseEvidence(ctx, questionID)
	if err != nil {
		return fmt.Errorf("load release evidence: %w", err)
	}
	return service.repository.Release(ctx, questionID, actorID, validationID, reviewID, reason)
}

func (service *Service) Quarantine(ctx context.Context, questionID, actorID uuid.UUID, reason string) error {
	return service.repository.Quarantine(ctx, questionID, actorID, reason)
}

func checkPassed(validation Validation, name string) bool {
	for _, item := range validation.Checks {
		if item.Name == name {
			return item.Passed
		}
	}
	return false
}

func replaceCheck(validation Validation, replacement Check) Validation {
	found := false
	validation.Passed = true
	for index := range validation.Checks {
		if validation.Checks[index].Name == replacement.Name {
			validation.Checks[index] = replacement
			found = true
		}
		validation.Passed = validation.Passed && validation.Checks[index].Passed
	}
	if !found {
		validation.Checks = append(validation.Checks, replacement)
		validation.Passed = validation.Passed && replacement.Passed
	}
	return validation
}
