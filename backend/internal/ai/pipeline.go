package ai

import "context"

// The §11.1 pipeline lives here. This file is the seam the HTTP layer compiles
// against; M4 step 2 replaces these bodies with the real stages and keeps the
// signatures.

func (s *Service) generate(ctx context.Context, req GenerationRequest) Outcome {
	_ = ctx
	_ = req
	return s.unavailable(req.Kind, s.Available())
}

func (s *Service) tutor(ctx context.Context, req TutorRequest) (TutorResponse, error) {
	_ = ctx
	return TutorResponse{}, newError(CodeUnavailable, "", "Tutoring is not available.", "")
}

func (s *Service) critique(ctx context.Context, req CritiqueRequest) (CritiqueResponse, error) {
	_ = ctx
	return CritiqueResponse{}, newError(CodeUnavailable, "", "Critique is not available.", "")
}

func (s *Service) reportDefect(ctx context.Context, rep DefectReport) (DefectVerdict, error) {
	_ = ctx
	return DefectVerdict{}, newError(CodeUnavailable, "", "Defect verification is not available.", "")
}

func (s *Service) applyPatch(ctx context.Context, dec PatchDecision) (PatchResult, error) {
	_ = ctx
	return PatchResult{}, newError(CodeUnavailable, "", "Patch application is not available.", "")
}
