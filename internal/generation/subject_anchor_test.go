package generation

import (
	"context"
	"errors"
	"testing"

	"gameasset/internal/crop"
)

// stubSubjectDetector is a programmable subject locator for tests.
type stubSubjectDetector struct {
	box   SubjectBox
	err   error
	calls int
}

func (s *stubSubjectDetector) Configured() bool { return true }
func (s *stubSubjectDetector) Detect(_ context.Context, _ []byte, _ string) (SubjectBox, error) {
	s.calls++
	if s.err != nil {
		return SubjectBox{}, s.err
	}
	return s.box, nil
}

// TestCoverCropOptions covers the anchor decision for the extreme-ratio cover
// crop: high-confidence detections anchor on the subject along the cropped axis;
// everything else (low confidence, error, no detector, ordinary ratio) falls
// back to a center cover crop.
func TestCoverCropOptions(t *testing.T) {
	ctx := context.Background()
	img := makePNG(2166, 726)

	t.Run("no detector → center cover", func(t *testing.T) {
		svc := &Service{}
		opts := svc.coverCropOptions(ctx, "t", img, "image/png", 2166, 726, 1008, 168)
		if opts.Mode != crop.ModeCover {
			t.Errorf("want ModeCover, got %q", opts.Mode)
		}
	})

	t.Run("ordinary ratio → center cover, no detect call", func(t *testing.T) {
		det := &stubSubjectDetector{box: SubjectBox{CenterX: 0.5, CenterY: 0.2, Confidence: 99}}
		svc := &Service{subject: det}
		// 16:9 is below the extreme threshold → must not even call the detector.
		opts := svc.coverCropOptions(ctx, "t", img, "image/png", 1920, 1080, 1280, 720)
		if opts.Mode != crop.ModeCover {
			t.Errorf("want ModeCover, got %q", opts.Mode)
		}
		if det.calls != 0 {
			t.Errorf("ordinary ratio should not call detector, got %d calls", det.calls)
		}
	})

	t.Run("wide banner high confidence → focal on Y", func(t *testing.T) {
		det := &stubSubjectDetector{box: SubjectBox{CenterX: 0.5, CenterY: 0.30, Confidence: 85}}
		svc := &Service{subject: det}
		opts := svc.coverCropOptions(ctx, "t", img, "image/png", 2166, 726, 1008, 168)
		if opts.Mode != crop.ModeFocal {
			t.Fatalf("want ModeFocal, got %q", opts.Mode)
		}
		if opts.FocalY != 0.30 {
			t.Errorf("wide banner should anchor FocalY=0.30, got %v", opts.FocalY)
		}
		if opts.FocalX != 0.5 {
			t.Errorf("wide banner should hold FocalX=0.5, got %v", opts.FocalX)
		}
	})

	t.Run("tall strip high confidence → focal on X", func(t *testing.T) {
		det := &stubSubjectDetector{box: SubjectBox{CenterX: 0.70, CenterY: 0.5, Confidence: 85}}
		svc := &Service{subject: det}
		opts := svc.coverCropOptions(ctx, "t", img, "image/png", 726, 2166, 168, 1008)
		if opts.Mode != crop.ModeFocal {
			t.Fatalf("want ModeFocal, got %q", opts.Mode)
		}
		if opts.FocalX != 0.70 {
			t.Errorf("tall strip should anchor FocalX=0.70, got %v", opts.FocalX)
		}
		if opts.FocalY != 0.5 {
			t.Errorf("tall strip should hold FocalY=0.5, got %v", opts.FocalY)
		}
	})

	t.Run("low confidence → center cover", func(t *testing.T) {
		det := &stubSubjectDetector{box: SubjectBox{CenterX: 0.5, CenterY: 0.1, Confidence: subjectConfidenceMin - 1}}
		svc := &Service{subject: det}
		opts := svc.coverCropOptions(ctx, "t", img, "image/png", 2166, 726, 1008, 168)
		if opts.Mode != crop.ModeCover {
			t.Errorf("low confidence should fall back to ModeCover, got %q", opts.Mode)
		}
		if det.calls != 1 {
			t.Errorf("extreme ratio should call detector once, got %d", det.calls)
		}
	})

	t.Run("detector error → center cover", func(t *testing.T) {
		det := &stubSubjectDetector{err: errors.New("boom")}
		svc := &Service{subject: det}
		opts := svc.coverCropOptions(ctx, "t", img, "image/png", 2166, 726, 1008, 168)
		if opts.Mode != crop.ModeCover {
			t.Errorf("detector error should fall back to ModeCover, got %q", opts.Mode)
		}
	})
}

// TestSetSubjectDetectorRejectsUnconfigured verifies the setter drops a nil or
// unconfigured detector so coverCropOptions stays on the center-crop path.
func TestSetSubjectDetectorRejectsUnconfigured(t *testing.T) {
	svc := &Service{}
	svc.SetSubjectDetector(nil)
	if svc.subject != nil {
		t.Error("nil detector should leave subject unset")
	}
	det := &stubSubjectDetector{box: SubjectBox{Confidence: 99}}
	svc.SetSubjectDetector(det)
	if svc.subject == nil {
		t.Error("configured detector should be installed")
	}
}
