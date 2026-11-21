package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// TooManyUploads is Upload's refusal of a person who has Max uploads that no ticket holds
// (Config.UploadsPerPerson). Kept is how long such an upload is kept (Config.UploadsKept; a
// negative duration: until a ticket holds it). errors.Is takes it for ErrTooManyUploads.
type TooManyUploads struct {
	Max  int
	Kept time.Duration
}

func (e *TooManyUploads) Error() string {
	return fmt.Sprintf("the person has %d uploads that no ticket holds, as many as the server keeps", e.Max)
}

func (e *TooManyUploads) Is(target error) bool { return target == ErrTooManyUploads }

// Upload keeps a file a person uploads, for a form to name (DESIGN 2.3, "Files"): it puts the file
// in Config.Files as the person's, at the server's time, and records it as an upload that no ticket
// holds yet. A person who has Config.UploadsPerPerson such uploads already is answered
// *TooManyUploads, and nothing is kept. It answers the file's ID.
func (s *Server) Upload(ctx context.Context, p Person, info FileInfo, r io.Reader) (string, error) {
	if s.cfg.Files == nil {
		return "", errors.New("server: no Config.Files to keep an upload in")
	}
	limit := max(s.cfg.UploadsPerPerson, 0)
	full := &TooManyUploads{Max: limit, Kept: s.cfg.UploadsKept}
	if limit > 0 {
		n, err := s.cfg.Store.OpenUploads(ctx, p.ID)
		if err != nil {
			return "", fmt.Errorf("the uploads: %w", err)
		}
		if n >= limit {
			return "", full
		}
	}
	info.By, info.At = p.ID, s.now()
	id, err := s.cfg.Files.Put(ctx, info, r)
	if err != nil {
		return "", err
	}
	// Two uploads of one person at once both pass the count above; the store counts again as it
	// records, so that the person never has more than the limit.
	if err := s.cfg.Store.KeepUpload(ctx, Upload{ID: id, By: p.ID, At: info.At}, limit); err != nil {
		if derr := s.cfg.Files.Delete(context.WithoutCancel(ctx), id); derr != nil && !errors.Is(derr, ErrNotFound) {
			s.log.Warn("deleting a file the store did not record as an upload", "id", id, "err", derr)
		}
		if errors.Is(err, ErrTooManyUploads) {
			return "", full
		}
		return "", fmt.Errorf("the uploads: %w", err)
	}
	return id, nil
}

// sweepBatch is how many uploads Sweep takes from the store at once; it forgets ten times as many
// operations at once.
const sweepBatch = 100

// Sweep lets go of the uploads that no ticket held in Config.UploadsKept and deletes their files,
// forgets the operations answered, or begun and never answered, more than Config.OperationsKept ago
// (DESIGN 4.4), and forgets the drafts of the organization's data kept more than Config.DraftsKept
// ago (DESIGN 13.9), which it logs. It answers how many files it deleted and how many operations it
// forgot, of both sorts. Run sweeps every Config.SweepEvery; any number of processes may sweep one
// store at once. A file the files fail to delete is logged, and tried again at the next sweep.
func (s *Server) Sweep(ctx context.Context) (files, operations int, err error) {
	now := s.now()
	if s.cfg.Files != nil && s.cfg.UploadsKept > 0 {
		for {
			ups, err := s.cfg.Store.LetGoUploads(ctx, now.Add(-s.cfg.UploadsKept), sweepBatch)
			if err != nil {
				return files, operations, fmt.Errorf("letting go of the uploads: %w", err)
			}
			var deleted []string
			for _, u := range ups {
				if err := s.cfg.Files.Delete(ctx, u.ID); err != nil && !errors.Is(err, ErrNotFound) {
					s.log.Warn("deleting an upload that no ticket held; it is tried again at the next sweep", "id", u.ID, "by", u.By, "err", err)
					continue
				}
				deleted = append(deleted, u.ID)
			}
			if len(deleted) > 0 {
				if err := s.cfg.Store.UploadsDeleted(ctx, deleted); err != nil {
					return files, operations, fmt.Errorf("recording the deleted uploads: %w", err)
				}
			}
			files += len(deleted)
			if len(ups) < sweepBatch || len(deleted) == 0 {
				break
			}
		}
	}
	if s.cfg.OperationsKept > 0 {
		for {
			n, err := s.cfg.Store.ForgetOperations(ctx, now.Add(-s.cfg.OperationsKept), 10*sweepBatch)
			if err != nil {
				return files, operations, fmt.Errorf("forgetting the operations: %w", err)
			}
			operations += n
			if n < 10*sweepBatch { // none of either sort is left (Store.ForgetOperations)
				break
			}
		}
	}
	if s.cfg.DraftsKept > 0 {
		// The drafts of the organization's data past their time (DESIGN 13.9).
		drafts := 0
		for {
			n, err := s.cfg.Store.ForgetDrafts(ctx, now.Add(-s.cfg.DraftsKept), 10*sweepBatch)
			if err != nil {
				return files, operations, fmt.Errorf("forgetting the drafts: %w", err)
			}
			drafts += n
			if n < 10*sweepBatch {
				break
			}
		}
		if drafts > 0 {
			s.log.Info("forgot the drafts of the organization's data past their time", "drafts", drafts)
		}
	}
	return files, operations, nil
}
