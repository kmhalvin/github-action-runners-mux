package dashboard

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kmhalvin/github-action-runners-mux/db/sqlc"
	"github.com/kmhalvin/github-action-runners-mux/pkg/mux"
)

type RunnerService struct {
	queries *sqlc.Queries
	mux     *mux.Multiplexer
}

func NewRunnerService(queries *sqlc.Queries, mx *mux.Multiplexer) *RunnerService {
	return &RunnerService{
		queries: queries,
		mux:     mx,
	}
}

type CombinedRunner struct {
	sqlc.Runner
	State         mux.RunnerState `json:"state"`
	ActiveWorkers int             `json:"active_workers"`
	Error         string          `json:"error,omitempty"`
	HasPat        bool            `json:"has_pat"`
	CanManage     bool            `json:"can_manage"`
	IsRegistered  bool            `json:"is_registered"`
}

func (s *RunnerService) ListRunners(ctx context.Context) ([]CombinedRunner, error) {
	runners, err := s.queries.ListRunners(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list runners: %w", err)
	}

	liveStatuses := s.mux.GetRunnerStatuses()
	statusMap := make(map[string]mux.RunnerStatus)
	for _, st := range liveStatuses {
		statusMap[st.Name] = st
	}

	var results []CombinedRunner
	for _, dbR := range runners {
		st, ok := statusMap[dbR.Name]
		if !ok {
			st = mux.RunnerStatus{State: mux.StateOffline}
		}
		
		isReg := false
		if dbR.Mode == "standalone" && dbR.Dir != "" {
			if _, err := os.Stat(filepath.Join(dbR.Dir, ".credentials")); err == nil {
				isReg = true
			}
		}

		hasPat := dbR.PAT != ""
		dbR.PAT = ""
		
		results = append(results, CombinedRunner{
			Runner:        dbR,
			State:         st.State,
			ActiveWorkers: st.ActiveWorkers,
			Error:         st.Error,
			HasPat:        hasPat,
			IsRegistered:  isReg,
		})
	}
	return results, nil
}

func (s *RunnerService) GetRunner(ctx context.Context, name string) (*CombinedRunner, error) {
	dbRunner, err := s.queries.GetRunnerByName(ctx, name)
	if err != nil {
		if strings.Contains(err.Error(), "no rows in result set") {
			return nil, mux.ErrRunnerNotFound
		}
		return nil, fmt.Errorf("db error: %w", err)
	}

	liveStatus := mux.RunnerStatus{State: mux.StateOffline}
	for _, st := range s.mux.GetRunnerStatuses() {
		if st.Name == name {
			liveStatus = st
			break
		}
	}

	isReg := false
	if dbRunner.Mode == "standalone" && dbRunner.Dir != "" {
		if _, err := os.Stat(filepath.Join(dbRunner.Dir, ".credentials")); err == nil {
			isReg = true
		}
	}

	hasPat := dbRunner.PAT != ""
	dbRunner.PAT = ""

	return &CombinedRunner{
		Runner:        dbRunner,
		State:         liveStatus.State,
		ActiveWorkers: liveStatus.ActiveWorkers,
		Error:         liveStatus.Error,
		HasPat:        hasPat,
		IsRegistered:  isReg,
	}, nil
}

func (s *RunnerService) CreateRunner(ctx context.Context, params sqlc.CreateRunnerParams, regToken string) (*sqlc.Runner, error) {
	dbRunner, err := s.queries.CreateRunner(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to save runner: %w", err)
	}

	err = s.mux.AddRunner(context.Background(), dbRunner, regToken)
	if err != nil {
		return &dbRunner, fmt.Errorf("failed to start runner: %w", err)
	}

	dbRunner.PAT = ""
	return &dbRunner, nil
}

type UpdateRunnerInput struct {
	PAT          string
	MaxRunners   int
	Labels       []string
	RunnerGroup  string
	RegToken     string
}

func (s *RunnerService) UpdateRunner(ctx context.Context, name string, input UpdateRunnerInput) (*sqlc.Runner, error) {
	dbRunner, err := s.queries.GetRunnerByName(ctx, name)
	if err != nil {
		if strings.Contains(err.Error(), "no rows in result set") {
			return nil, mux.ErrRunnerNotFound
		}
		return nil, fmt.Errorf("db error: %w", err)
	}

	// Check if runner is currently running/registered — don't allow edit if live
	for _, st := range s.mux.GetRunnerStatuses() {
		if st.Name == name && (st.State == mux.StateOnline || st.State == mux.StateBusy || st.State == mux.StateRegistering || st.State == mux.StatePaused || st.State == mux.StateDraining) {
			return nil, fmt.Errorf("cannot edit runner %s while it is %s — remove and recreate instead", name, st.State)
		}
	}

	alreadyRegistered := false
	if dbRunner.Mode == "standalone" && dbRunner.Dir != "" {
		if _, err := os.Stat(filepath.Join(dbRunner.Dir, ".credentials")); err == nil {
			alreadyRegistered = true
		}
	}

	var updatedRunner sqlc.Runner
	if alreadyRegistered && input.PAT == "" && input.MaxRunners == 0 && input.RunnerGroup == "" && len(input.Labels) == 0 {
		updatedRunner = dbRunner
	} else {
		updateParams := sqlc.UpdateRunnerParams{
			ID: dbRunner.ID,
		}
		if input.PAT != "" {
			updateParams.PAT = sql.NullString{String: input.PAT, Valid: true}
		}
		if input.MaxRunners > 0 {
			updateParams.MaxRunners = sql.NullInt64{Int64: int64(input.MaxRunners), Valid: true}
		}
		if input.RunnerGroup != "" {
			updateParams.RunnerGroup = sql.NullString{String: input.RunnerGroup, Valid: true}
		}
		if len(input.Labels) > 0 {
			updateParams.Labels = sql.NullString{String: strings.Join(input.Labels, ","), Valid: true}
		}

		updatedRunner, err = s.queries.UpdateRunner(ctx, updateParams)
		if err != nil {
			return nil, fmt.Errorf("failed to update runner: %w", err)
		}
	}

	err = s.mux.AddRunner(context.Background(), updatedRunner, input.RegToken)
	if err != nil {
		return nil, fmt.Errorf("failed to start runner: %w", err)
	}

	updatedRunner.PAT = ""
	return &updatedRunner, nil
}

func (s *RunnerService) DeleteRunner(ctx context.Context, name string, force bool, deregToken string) error {
	dbRunner, err := s.queries.GetRunnerByName(ctx, name)
	if err != nil {
		if strings.Contains(err.Error(), "no rows in result set") {
			return mux.ErrRunnerNotFound
		}
		return fmt.Errorf("db error: %w", err)
	}

	// Tell mux to stop it
	_ = s.mux.RemoveRunner(context.Background(), name, force, dbRunner.Mode)

	// Delete from DB
	err = s.queries.DeleteRunnerByName(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to delete from db: %w", err)
	}

	go func(rName, rMode, token string, runner sqlc.Runner) {
		time.Sleep(2 * time.Second)
		_ = s.mux.RemoveRunner(context.Background(), rName, true, rMode)

		_ = s.mux.Deregister(runner, token)
	}(name, dbRunner.Mode, deregToken, dbRunner)

	return nil
}
