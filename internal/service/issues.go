// Package service holds the forge's use cases (see service.go). This file
// holds the issue tracker: labels, issues, comments and milestones.
package service

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"forge/internal/domain"
)

// ErrUnknownLabel is returned when an issue names a label the repository
// does not have (the REST API answers 422).
var ErrUnknownLabel = errors.New("unknown label")

// --- Labels ---

// CreateLabel adds a label to a repository.
func (s *Service) CreateLabel(owner, repo, name, color string) (*domain.Label, error) {
	if _, err := s.store.GetRepository(owner, repo); err != nil {
		return nil, err
	}
	label := &domain.Label{Name: name, Color: color}
	if err := s.store.CreateLabel(owner, repo, label); err != nil {
		return nil, err
	}
	return label, nil
}

// ListLabels returns a repository's labels, by name.
func (s *Service) ListLabels(owner, repo string) ([]*domain.Label, error) {
	return s.store.ListLabels(owner, repo)
}

// LabelColors returns a repository's labels as a name -> colour map, for
// rendering an issue's labels.
func (s *Service) LabelColors(owner, repo string) (map[string]string, error) {
	labels, err := s.store.ListLabels(owner, repo)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, l := range labels {
		out[l.Name] = l.Color
	}
	return out, nil
}

// UpdateLabel renames and/or recolours a label.
func (s *Service) UpdateLabel(owner, repo, oldName, name, color string) (*domain.Label, error) {
	if _, err := s.store.GetRepository(owner, repo); err != nil {
		return nil, err
	}
	label := &domain.Label{Name: name, Color: color}
	if err := s.store.UpdateLabel(owner, repo, oldName, label); err != nil {
		return nil, err
	}
	return label, nil
}

// DeleteLabel removes a label from a repository.
func (s *Service) DeleteLabel(owner, repo, name string) error {
	if _, err := s.store.GetRepository(owner, repo); err != nil {
		return err
	}
	return s.store.DeleteLabel(owner, repo, name)
}

// --- Issues ---

// CreateIssueInput is what CreateIssue needs.
type CreateIssueInput struct {
	Title  string
	Body   string
	Labels []string
	Author string
}

// CreateIssue creates an issue. Every label it names must already exist on
// the repository, or the create is refused with ErrUnknownLabel.
func (s *Service) CreateIssue(owner, repo string, in CreateIssueInput) (*domain.Issue, error) {
	if _, err := s.store.GetRepository(owner, repo); err != nil {
		return nil, err
	}
	for _, name := range in.Labels {
		ok, err := s.store.HasLabel(owner, repo, name)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownLabel, name)
		}
	}
	issue := &domain.Issue{
		RepoOwner: owner,
		RepoName:  repo,
		Title:     in.Title,
		Body:      in.Body,
		State:     domain.IssueOpen,
		Labels:    append([]string(nil), in.Labels...),
		Author:    in.Author,
	}
	if err := s.store.CreateIssue(issue); err != nil {
		return nil, err
	}
	return issue, nil
}

// GetIssue returns one issue by number.
func (s *Service) GetIssue(owner, repo string, number int) (*domain.Issue, error) {
	return s.store.GetIssue(owner, repo, number)
}

// IssueByID returns an issue by its global numeric id.
func (s *Service) IssueByID(id int64) (*domain.Issue, error) {
	return s.store.GetIssueByID(id)
}

// IssueListOptions selects and orders an issue list.
type IssueListOptions struct {
	State     string // "open", "closed", "all"; empty means "open"
	Sort      string // "created" (the only key the suite uses)
	Direction string // "asc" or "desc"; empty means "desc"
}

// ListIssues returns a repository's issues, filtered and ordered. Ordering
// is by creation time with the number as the tiebreak, so two issues made
// in the same instant still come out in creation order.
func (s *Service) ListIssues(owner, repo string, opts IssueListOptions) ([]*domain.Issue, error) {
	all, err := s.store.ListIssues(owner, repo)
	if err != nil {
		return nil, err
	}
	state := opts.State
	if state == "" {
		state = "open"
	}
	out := []*domain.Issue{}
	for _, issue := range all {
		switch state {
		case "all":
		case "open":
			if issue.State != domain.IssueOpen {
				continue
			}
		case "closed":
			if issue.State != domain.IssueClosed {
				continue
			}
		default:
			if issue.State != domain.IssueOpen {
				continue
			}
		}
		out = append(out, issue)
	}
	asc := opts.Direction == "asc"
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			if asc {
				return out[i].CreatedAt.Before(out[j].CreatedAt)
			}
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		if asc {
			return out[i].Number < out[j].Number
		}
		return out[i].Number > out[j].Number
	})
	return out, nil
}

// UpdateIssueInput is what UpdateIssue may change; a nil field is left alone.
type UpdateIssueInput struct {
	Title *string
	Body  *string
	State *string
}

// UpdateIssue patches an issue's title, body and/or state.
func (s *Service) UpdateIssue(owner, repo string, number int, in UpdateIssueInput) (*domain.Issue, error) {
	issue, err := s.store.GetIssue(owner, repo, number)
	if err != nil {
		return nil, err
	}
	if in.Title != nil {
		issue.Title = *in.Title
	}
	if in.Body != nil {
		issue.Body = *in.Body
	}
	if in.State != nil {
		switch *in.State {
		case "open":
			issue.State = domain.IssueOpen
			issue.ClosedAt = nil
		case "closed":
			if issue.State != domain.IssueClosed {
				now := time.Now().UTC()
				issue.ClosedAt = &now
			}
			issue.State = domain.IssueClosed
		default:
			return nil, fmt.Errorf("unknown issue state %q", *in.State)
		}
	}
	if err := s.store.UpdateIssue(issue); err != nil {
		return nil, err
	}
	return issue, nil
}

// --- Comments ---

// CreateComment appends a comment to an issue.
func (s *Service) CreateComment(owner, repo string, issueNumber int, body, author string) (*domain.Comment, error) {
	c := &domain.Comment{
		RepoOwner:   owner,
		RepoName:    repo,
		IssueNumber: issueNumber,
		Body:        body,
		Author:      author,
	}
	if err := s.store.CreateComment(c); err != nil {
		return nil, err
	}
	return c, nil
}

// ListComments returns an issue's comments in creation order.
func (s *Service) ListComments(owner, repo string, issueNumber int) ([]*domain.Comment, error) {
	return s.store.ListComments(owner, repo, issueNumber)
}

// CommentByID returns a comment by its global numeric id.
func (s *Service) CommentByID(id int64) (*domain.Comment, error) {
	return s.store.GetCommentByID(id)
}

// --- Milestones ---

// CreateMilestoneInput is what CreateMilestone needs.
type CreateMilestoneInput struct {
	Title       string
	Description string
	State       string
	DueOn       *time.Time
}

// CreateMilestone creates a milestone on a repository.
func (s *Service) CreateMilestone(owner, repo string, in CreateMilestoneInput) (*domain.Milestone, error) {
	if _, err := s.store.GetRepository(owner, repo); err != nil {
		return nil, err
	}
	state := domain.MilestoneOpen
	if in.State == "closed" {
		state = domain.MilestoneClosed
	}
	m := &domain.Milestone{
		RepoOwner:   owner,
		RepoName:    repo,
		Title:       in.Title,
		Description: in.Description,
		State:       state,
		DueOn:       in.DueOn,
	}
	if err := s.store.CreateMilestone(m); err != nil {
		return nil, err
	}
	return m, nil
}

// GetMilestone returns one milestone by number.
func (s *Service) GetMilestone(owner, repo string, number int) (*domain.Milestone, error) {
	return s.store.GetMilestone(owner, repo, number)
}

// ListMilestones returns a repository's milestones, by number. When state
// is "open" or "closed" only milestones in that state are returned; an
// empty state returns all of them.
func (s *Service) ListMilestones(owner, repo, state string) ([]*domain.Milestone, error) {
	all, err := s.store.ListMilestones(owner, repo)
	if err != nil {
		return nil, err
	}
	if state == "" || state == "all" {
		return all, nil
	}
	out := []*domain.Milestone{}
	for _, m := range all {
		if string(m.State) == state {
			out = append(out, m)
		}
	}
	return out, nil
}

// UpdateMilestoneInput is what UpdateMilestone may change; a nil field is
// left alone.
type UpdateMilestoneInput struct {
	Title       *string
	Description *string
	State       *string
	DueOn       *time.Time
	SetDueOn    bool
}

// UpdateMilestone patches a milestone.
func (s *Service) UpdateMilestone(owner, repo string, number int, in UpdateMilestoneInput) (*domain.Milestone, error) {
	m, err := s.store.GetMilestone(owner, repo, number)
	if err != nil {
		return nil, err
	}
	if in.Title != nil {
		m.Title = *in.Title
	}
	if in.Description != nil {
		m.Description = *in.Description
	}
	if in.State != nil {
		switch *in.State {
		case "open":
			m.State = domain.MilestoneOpen
			m.ClosedAt = nil
		case "closed":
			if m.State != domain.MilestoneClosed {
				now := time.Now().UTC()
				m.ClosedAt = &now
			}
			m.State = domain.MilestoneClosed
		default:
			return nil, fmt.Errorf("unknown milestone state %q", *in.State)
		}
	}
	if in.SetDueOn {
		m.DueOn = in.DueOn
	}
	if err := s.store.UpdateMilestone(m); err != nil {
		return nil, err
	}
	return m, nil
}

// DeleteMilestone removes a milestone from a repository.
func (s *Service) DeleteMilestone(owner, repo string, number int) error {
	if _, err := s.store.GetRepository(owner, repo); err != nil {
		return err
	}
	return s.store.DeleteMilestone(owner, repo, number)
}
