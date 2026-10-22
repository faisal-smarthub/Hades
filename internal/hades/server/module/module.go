// Package module implements the ModuleService ConnectRPC handler. It handles
// module creation (including the initial Gitaly repository and commit), listing,
// and lookup. Module creation uses a unit-of-work with saga-style compensation:
// if the DB transaction fails after the Gitaly repository is created, the
// repository is deleted before returning the error.
package module

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	identityv1 "github.com/alipourhabibi/Hades/api/gen/api/identity/v1"
	registrypbv1 "github.com/alipourhabibi/Hades/api/gen/api/registry/v1"
	registryv1 "github.com/alipourhabibi/Hades/api/gen/api/registry/v1/registryv1connect"
	"github.com/alipourhabibi/Hades/internal/hades/constants"
	"github.com/alipourhabibi/Hades/internal/hades/server"
	"github.com/alipourhabibi/Hades/internal/hades/storage/db"
	commitdb "github.com/alipourhabibi/Hades/internal/hades/storage/db/commit"
	"github.com/alipourhabibi/Hades/internal/hades/storage/db/gitalyoplog"
	gitstorage "github.com/alipourhabibi/Hades/internal/hades/storage/git"
	"github.com/alipourhabibi/Hades/utils/connerr"
	"github.com/alipourhabibi/Hades/utils/log"
	"github.com/google/uuid"
)

// recordOpLogFailure marks a pending Gitaly operation-log entry as failed.
// Best effort: the log exists so a crashed operation can be compensated later,
// and a failure to update it must not mask the original error.
func (s *Server) recordOpLogFailure(ctx context.Context, logID uuid.UUID, cause error) {
	if s.gitalyOpLog == nil || logID == uuid.Nil {
		return
	}
	if err := s.gitalyOpLog.UpdateStatus(ctx, logID, gitalyoplog.StatusFailed, "", cause.Error()); err != nil {
		s.logger.Error("failed to update gitaly op log", "error", err, "log_id", logID)
	}
}

// orgStorage is the subset of org.Storage used by the Server.
type orgStorage interface {
	GetByName(ctx context.Context, name string) (*identityv1.User, error)
}

// moduleStorage is the subset of ModuleStorage used by the Server.
type moduleStorage interface {
	GetModulesByRefs(ctx context.Context, refs ...*registrypbv1.ModuleRef) ([]*registrypbv1.Module, error)
	ListVisibleModules(ctx context.Context, ownerUsername, subject, subjectID string, limit, offset int) ([]*registrypbv1.Module, error)
	GetModuleByOwnerAndName(ctx context.Context, owner, name string) (*registrypbv1.Module, error)
	Create(ctx context.Context, name, ownerId string, visibility registrypbv1.ModuleVisibility, state registrypbv1.ModuleState, description, url, defaultLabelName, defaultBranch string, lintPreset registrypbv1.LintPreset, breakingEnabled bool) (*registrypbv1.Module, error)
	Update(ctx context.Context, req *registrypbv1.UpdateModuleRequest) (*registrypbv1.Module, error)
	Delete(ctx context.Context, moduleID string) error
}

// authService is the subset of the authorization Server used by the Server.
type authService interface {
	CheckReadAccess(ctx context.Context, user *identityv1.User, modules []*registrypbv1.Module) error
	Can(ctx context.Context, in *constants.Policy) (*constants.CanResponse, error)
}

type Server struct {
	registryv1.UnimplementedModuleServiceHandler

	logger          *log.LoggerWrapper
	registryHost    string
	moduleDBStorage moduleStorage
	commitDBStorage commitdb.Storage
	gitStorage      gitstorage.Storage
	authorization   authService
	orgDBStorage    orgStorage
	uow             db.UnitOfWork
	gitalyOpLog     gitalyoplog.Storage
}

func NewServer(deps *server.Dependencies) *Server {
	return &Server{
		logger:          deps.Logger,
		registryHost:    deps.RegistryHost,
		moduleDBStorage: deps.ModuleDB,
		commitDBStorage: deps.CommitDB,
		gitStorage:      deps.GitStorage,
		authorization:   deps.Authorization,
		orgDBStorage:    deps.OrgDB,
		uow:             deps.UoW,
		gitalyOpLog:     deps.GitalyOpLog,
	}
}

// GetModules returns modules matching the given refs, enforcing read access for private ones.
// user may be nil (anonymous): public modules are returned; private ones produce NotFound.
func (s *Server) GetModules(ctx context.Context, refs []*registrypbv1.ModuleRef) ([]*registrypbv1.Module, error) {
	user, _ := ctx.Value(constants.ContextKeyUser).(*identityv1.User) // nil for anonymous
	modules, err := s.moduleDBStorage.GetModulesByRefs(ctx, refs...)
	if err != nil {
		return nil, err
	}
	// Nothing is returned alongside the error. Returning the modules and the
	// access error together made correctness depend on every caller checking the
	// error first, and one call site that did not would disclose private modules.
	if err := s.authorization.CheckReadAccess(ctx, user, modules); err != nil {
		return nil, err
	}
	return modules, nil
}

func (s *Server) ListModules(ctx context.Context, in *connect.Request[registrypbv1.ListModulesRequest]) (*connect.Response[registrypbv1.ListModulesResponse], error) {
	// user may be nil when called without an Authorization header (anonymous access).
	// Anonymous callers receive only public modules; authenticated callers receive
	// public modules plus any private modules they are authorised to read.
	user, _ := ctx.Value(constants.ContextKeyUser).(*identityv1.User)

	pageSize, offset := server.Page(in.Msg.PageSize, in.Msg.PageToken)

	userID := "anonymous"
	if user != nil {
		userID = user.Id
	}

	// The query itself narrows to what the caller can plausibly read: public
	// modules, their own, and anything covered by one of their role bindings.
	// CheckReadAccess still runs on every row because the OPA policy, not the
	// SQL, is the authority; the pre-filter exists so that a page is not mostly
	// discarded afterwards.
	//
	// The batch loop remains as the backstop for the rare row the pre-filter
	// admits but the policy rejects, so a page is still the size the caller
	// asked for. In practice it now completes on the first iteration.
	subject, subjectID := "", ""
	if user != nil {
		subject, subjectID = user.Username, user.Id
	}

	visible := make([]*registrypbv1.Module, 0, pageSize)
	scanOffset := offset
	exhausted := false
	for batch := 0; batch < maxListScanBatches && len(visible) < pageSize; batch++ {
		modules, err := s.moduleDBStorage.ListVisibleModules(ctx, in.Msg.Owner, subject, subjectID, pageSize, scanOffset)
		if err != nil {
			s.logger.Error("failed to list modules", "error", err, "procedure", "ListModules", "user_id", userID)
			return nil, connerr.FromDB(err)
		}
		if len(modules) == 0 {
			exhausted = true
			break
		}
		for _, m := range modules {
			scanOffset++
			// CheckReadAccess reports private modules the caller cannot read as
			// NotFound, which is what hides their existence. List semantics are
			// to filter rather than fail on the first denied module.
			if err := s.authorization.CheckReadAccess(ctx, user, []*registrypbv1.Module{m}); err == nil {
				visible = append(visible, m)
				if len(visible) == pageSize {
					break
				}
			}
		}
		if len(modules) < pageSize {
			exhausted = true
			break
		}
	}

	nextPageToken := ""
	if !exhausted {
		nextPageToken = strconv.Itoa(scanOffset)
	}

	return &connect.Response[registrypbv1.ListModulesResponse]{
		Msg: &registrypbv1.ListModulesResponse{Modules: visible, NextPageToken: nextPageToken},
	}, nil
}

// maxListScanBatches bounds how many raw pages ListModules will read while
// trying to fill one visible page. The SQL pre-filter normally makes one batch
// enough; this bounds the pathological case where the policy rejects rows the
// pre-filter admitted.
const maxListScanBatches = 10

func (s *Server) GetModule(ctx context.Context, in *connect.Request[registrypbv1.GetModuleRequest]) (*connect.Response[registrypbv1.GetModuleResponse], error) {
	// user may be nil for anonymous access; CheckReadAccess handles the nil case.
	user, _ := ctx.Value(constants.ContextKeyUser).(*identityv1.User)

	m, err := s.moduleDBStorage.GetModuleByOwnerAndName(ctx, in.Msg.Owner, in.Msg.Name)
	if err != nil {
		userID := "anonymous"
		if user != nil {
			userID = user.Id
		}
		s.logger.Warn("module not found", "procedure", "GetModule", "user_id", userID, "owner", in.Msg.Owner, "name", in.Msg.Name, "error", err)
		return nil, connerr.NotFound("module not found")
	}

	if err := s.authorization.CheckReadAccess(ctx, user, []*registrypbv1.Module{m}); err != nil {
		// Surface as not-found so as not to leak existence of private modules.
		return nil, connerr.NotFound("module not found")
	}

	return &connect.Response[registrypbv1.GetModuleResponse]{
		Msg: &registrypbv1.GetModuleResponse{Module: m},
	}, nil
}

func (s *Server) CreateModuleByName(ctx context.Context, in *connect.Request[registrypbv1.CreateModuleByNameRequest]) (*connect.Response[registrypbv1.CreateModuleByNameResponse], error) {
	in.Msg.Name = strings.ToLower(strings.TrimSpace(in.Msg.Name))
	// Before any storage or git call. The module name becomes the second
	// segment of every URL the module appears at and the second segment of its
	// repository path on disk, and neither was checked: "../escape" created a
	// repository outside the owner's namespace and "has space" created a
	// database row with no repository behind it. See constants.ValidateName.
	if err := constants.ValidateName(in.Msg.Name); err != nil {
		return nil, connerr.InvalidArgument("module " + err.Error())
	}
	if in.Msg.DefaultBranch == "" {
		in.Msg.DefaultBranch = "main"
	}

	user, ok := ctx.Value(constants.ContextKeyUser).(*identityv1.User)
	if !ok {
		s.logger.Error("missing user in context", "procedure", "CreateModuleByName")
		return nil, connerr.Unauthenticated("not authenticated")
	}

	ownerName, ownerID, err := s.resolveOwnerByName(ctx, user, in.Msg.Owner)
	if err != nil {
		return nil, err
	}

	lintPreset := in.Msg.LintPreset
	if lintPreset == registrypbv1.LintPreset_LINT_PRESET_UNSPECIFIED {
		lintPreset = registrypbv1.LintPreset_LINT_PRESET_DEFAULT
	}

	module, err := s.createModule(ctx, user, createModuleParams{
		ownerName:       ownerName,
		ownerID:         ownerID,
		name:            in.Msg.Name,
		visibility:      in.Msg.Visibility,
		description:     in.Msg.Description,
		defaultBranch:   in.Msg.DefaultBranch,
		lintPreset:      lintPreset,
		breakingEnabled: in.Msg.BreakingEnabled,
	})
	if err != nil {
		return nil, err
	}

	return &connect.Response[registrypbv1.CreateModuleByNameResponse]{
		Msg: &registrypbv1.CreateModuleByNameResponse{
			Module: module,
		},
	}, nil
}

// CreateModule creates a single module in ownerName's namespace. It is the
// entry point for the buf.build CreateModules protocol adapter (which has no
// single-request shape of its own — it batches several of these), sharing
// the same validation, authorization, and creation path as CreateModuleByName.
//
// ownerName may name an organisation instead of defaulting to the caller: the
// buf CLI's "--create" flow resolves the owner straight from the module's
// configured full name (e.g. "smarthub" in "buf.smarthubai.net/smarthub/
// infer-metricquery-svc"), so a service's protos land in the org's namespace
// rather than the pushing user's personal one.
func (s *Server) CreateModule(ctx context.Context, ownerName, name string, visibility registrypbv1.ModuleVisibility) (*registrypbv1.Module, error) {
	user, ok := ctx.Value(constants.ContextKeyUser).(*identityv1.User)
	if !ok {
		s.logger.Error("missing user in context", "procedure", "CreateModule")
		return nil, connerr.Unauthenticated("not authenticated")
	}

	name = strings.ToLower(strings.TrimSpace(name))
	if err := constants.ValidateName(name); err != nil {
		return nil, connerr.InvalidArgument("module " + err.Error())
	}

	resolvedOwnerName, ownerID, err := s.resolveOwnerByName(ctx, user, ownerName)
	if err != nil {
		return nil, err
	}

	return s.createModule(ctx, user, createModuleParams{
		ownerName:     resolvedOwnerName,
		ownerID:       ownerID,
		name:          name,
		visibility:    visibility,
		defaultBranch: "main",
		lintPreset:    registrypbv1.LintPreset_LINT_PRESET_DEFAULT,
	})
}

// resolveOwnerByName resolves an owner name to the namespace that will own a
// module, defaulting to the caller's own username when ownerNameInput is
// empty. Naming an organisation here creates the module in that org's
// namespace, which the CREATE policy check in createModule then authorises:
// org membership grants bindings over "<org>/*", so no policy change is
// needed to support it.
func (s *Server) resolveOwnerByName(ctx context.Context, user *identityv1.User, ownerNameInput string) (ownerName, ownerID string, err error) {
	ownerName = strings.ToLower(strings.TrimSpace(ownerNameInput))
	if ownerName == "" || ownerName == user.Username {
		return user.Username, user.Id, nil
	}
	org, err := s.orgDBStorage.GetByName(ctx, ownerName)
	if err != nil {
		s.logger.Warn("module owner namespace not found", "procedure", "CreateModule", "user_id", user.Id, "owner", ownerName)
		return "", "", connerr.NotFound("owner namespace not found")
	}
	return ownerName, org.Id, nil
}

// createModuleParams groups the inputs to createModule so both the native
// CreateModuleByName RPC and the buf.build CreateModules protocol adapter can
// share the same creation path.
type createModuleParams struct {
	ownerName       string
	ownerID         string
	name            string
	visibility      registrypbv1.ModuleVisibility
	description     string
	defaultBranch   string
	lintPreset      registrypbv1.LintPreset
	breakingEnabled bool
}

// createModule runs the module-creation sequence shared by CreateModuleByName
// and CreateModule: the CREATE policy check, the name-collision check, the
// Gitaly repository and initial commit, and finally the DB transaction
// recording the module and its first commit.
func (s *Server) createModule(ctx context.Context, user *identityv1.User, p createModuleParams) (*registrypbv1.Module, error) {
	moduleFullName := p.ownerName + "/" + p.name

	can, err := s.authorization.Can(ctx, &constants.Policy{
		Subject:      user.Username,
		ResourceType: string(constants.ResourceModule),
		Action:       string(constants.CREATE),
		Domain:       moduleFullName,
	})
	if err != nil {
		return nil, err
	}
	if !can.Allowed {
		s.logger.Warn("user not allowed to create module", "procedure", "CreateModule", "user_id", user.Id, "module", moduleFullName)
		return nil, connerr.PermissionDenied("user is not allowed to create this repo")
	}

	// Answer "this name is taken" before doing any git work.
	//
	// Creating the same module twice used to return Internal. The unique
	// constraint is on the database row, and the git steps run first: the
	// repository already existed, CreateRepository is idempotent, and PutFiles
	// then refused with ErrRefMoved because it expected no branch. That error
	// reached the interceptor untranslated and was flattened, so a client could
	// not tell "this name is taken" from "the server is broken" and a retry
	// loop retried something that would never succeed.
	//
	// The database constraint is still the authority: this is a check, not a
	// lock, and two concurrent creates can both pass it. The one that loses
	// gets AlreadyExists from connerr.FromDB inside the transaction below.
	if existing, err := s.moduleDBStorage.GetModuleByOwnerAndName(ctx, p.ownerName, p.name); err == nil && existing != nil {
		return nil, connerr.AlreadyExists("module " + moduleFullName + " already exists")
	}

	bsrName := moduleFullName
	if s.registryHost != "" {
		bsrName = s.registryHost + "/" + moduleFullName
	}
	bufYAML := fmt.Sprintf("version: v2\nmodules:\n  - path: .\n    name: %s\nlint:\n  use:\n    - %s\n", bsrName, lintPresetToRule(p.lintPreset))
	if p.breakingEnabled {
		bufYAML += "breaking:\n  use:\n    - FILE\n"
	}

	initialFiles := []*registrypbv1.File{
		{Path: "README.md", Content: []byte("")},
		{Path: "buf.yaml", Content: []byte(bufYAML)},
	}
	// Write a 'pending' log entry (auto-committed, outside the UoW) so that the
	// background cleanup job can compensate if the server crashes mid-operation.
	var logID uuid.UUID
	if s.gitalyOpLog != nil {
		logID, _ = s.gitalyOpLog.CreatePending(ctx, gitalyoplog.OpCreateModule, moduleFullName, user.Id)
	}

	// Git work first, then a short DB transaction, matching the ordering settled
	// for Upload: a Gitaly RPC inside an open transaction holds a database
	// connection across two network round trips.
	//
	// Order: (1) Gitaly CreateRepository → (2) Gitaly UserCommitFiles →
	//        (3) one transaction inserting the module and commit rows.
	//
	// Compensation is saga-style: if any later step fails, the repository
	// created in (1) is deleted. The pending gitalyOpLog entry written above
	// lets the cleanup job compensate if the process dies mid-sequence.
	if err := s.gitStorage.CreateRepository(ctx, moduleFullName, p.defaultBranch); err != nil {
		s.recordOpLogFailure(ctx, logID, err)
		return nil, err
	}

	gitFiles := make([]*gitstorage.File, len(initialFiles))
	for i, f := range initialFiles {
		gitFiles[i] = &gitstorage.File{Path: f.Path, Content: f.Content}
	}
	// ExpectedHead is empty: this is the first commit in a repository that was
	// created a few lines above, so the branch must not exist yet. If it does,
	// something else raced us and PutFiles refuses rather than overwriting.
	commitHash, err := s.gitStorage.PutFiles(ctx, gitstorage.PutFilesRequest{
		RepoPath:    moduleFullName,
		Branch:      p.defaultBranch,
		Files:       gitFiles,
		AuthorName:  user.Username,
		AuthorEmail: user.Email,
		Message:     "initial commit",
	})
	if err != nil {
		// The repository is not deleted when the branch already existed: it
		// belongs to a module someone else created, and removing it would turn
		// a duplicate-name error into data loss.
		if errors.Is(err, gitstorage.ErrRefMoved) {
			s.recordOpLogFailure(ctx, logID, err)
			return nil, connerr.AlreadyExists("module " + moduleFullName + " already exists")
		}
		_ = s.gitStorage.DeleteRepository(ctx, moduleFullName)
		s.recordOpLogFailure(ctx, logID, err)
		return nil, err
	}

	result, err := s.uow.Do(ctx, func(txCtx context.Context) (interface{}, error) {
		module, err := s.moduleDBStorage.Create(
			txCtx,
			moduleFullName,
			p.ownerID,
			p.visibility,
			registrypbv1.ModuleState_MODULE_STATE_ACTIVE,
			p.description,
			"",
			"",
			p.defaultBranch,
			p.lintPreset,
			p.breakingEnabled,
		)
		if err != nil {
			return nil, connerr.FromDB(err)
		}

		if err := s.commitDBStorage.Create(
			txCtx,
			uuid.New(),
			commitHash,
			user.Id,
			module.Id,
			registrypbv1.DigestType_DIGEST_TYPE_B5,
			"",
			user.Id,
			"",
		); err != nil {
			return nil, connerr.FromDB(err)
		}

		return module, nil
	}, 30*time.Second)
	if err != nil {
		// The DB rolled itself back; undo the git side to match. A failure to
		// compensate is logged rather than dropped: it leaves an orphan
		// repository that will collide with the next attempt to create a module
		// with this name, and the failure would otherwise surface there, far
		// from its cause.
		if delErr := s.gitStorage.DeleteRepository(ctx, moduleFullName); delErr != nil {
			s.logger.Error("failed to remove the repository after a failed module creation",
				"error", delErr, "procedure", "CreateModule", "module", moduleFullName,
				"cause", err)
			if s.gitalyOpLog != nil && logID != uuid.Nil {
				_ = s.gitalyOpLog.UpdateStatus(ctx, logID, gitalyoplog.StatusFailed, "",
					"module creation failed and the repository could not be removed: "+delErr.Error())
			}
		}
	}

	// Update the operation log (auto-committed, outside the UoW).
	if s.gitalyOpLog != nil && logID != uuid.Nil {
		status := gitalyoplog.StatusCompleted
		errReason := ""
		if err != nil {
			status = gitalyoplog.StatusFailed
			errReason = err.Error()
		}
		_ = s.gitalyOpLog.UpdateStatus(ctx, logID, status, "", errReason)
	}

	if err != nil {
		return nil, err
	}

	module, ok := result.(*registrypbv1.Module)
	if !ok {
		s.logger.Error("unexpected result type from the create-module transaction",
			"procedure", "CreateModule", "module", moduleFullName)
		return nil, connerr.Internal("failed to create module")
	}

	return module, nil
}

func (s *Server) UpdateModule(ctx context.Context, in *connect.Request[registrypbv1.UpdateModuleRequest]) (*connect.Response[registrypbv1.UpdateModuleResponse], error) {
	user, ok := ctx.Value(constants.ContextKeyUser).(*identityv1.User)
	if !ok {
		s.logger.Error("missing user in context", "procedure", "UpdateModule")
		return nil, connerr.Unauthenticated("not authenticated")
	}

	moduleFullName := in.Msg.Owner + "/" + in.Msg.Name

	can, err := s.authorization.Can(ctx, &constants.Policy{
		Subject:      user.Username,
		ResourceType: string(constants.ResourceModule),
		Action:       string(constants.ActionUpdate),
		Domain:       moduleFullName,
	})
	if err != nil {
		return nil, err
	}
	if !can.Allowed {
		s.logger.Warn("user not allowed to update module", "procedure", "UpdateModule", "user_id", user.Id, "module", moduleFullName)
		return nil, connerr.PermissionDenied("user is not allowed to update this module")
	}

	// Making a private module public is checked separately.
	//
	// module:update covers editing a description; publishing a private schema
	// to the world is a materially different decision, and a contributor who
	// legitimately holds update rights should not necessarily hold it. Only the
	// private-to-public direction needs the extra permission: making a public
	// module private removes access rather than granting it.
	if in.Msg.Visibility != nil && *in.Msg.Visibility == registrypbv1.ModuleVisibility_MODULE_VISIBILITY_PUBLIC {
		current, err := s.moduleDBStorage.GetModuleByOwnerAndName(ctx, in.Msg.Owner, in.Msg.Name)
		if err != nil {
			s.logger.Error("failed to read the module before a visibility change", "error", err,
				"procedure", "UpdateModule", "module", moduleFullName)
			return nil, connerr.FromDB(err)
		}
		if current.Visibility != registrypbv1.ModuleVisibility_MODULE_VISIBILITY_PUBLIC {
			canPublish, err := s.authorization.Can(ctx, &constants.Policy{
				Subject:      user.Username,
				ResourceType: string(constants.ResourceModule),
				Action:       string(constants.ActionPublish),
				Domain:       moduleFullName,
			})
			if err != nil {
				return nil, err
			}
			if !canPublish.Allowed {
				s.logger.Warn("user not allowed to publish module", "procedure", "UpdateModule",
					"user_id", user.Id, "module", moduleFullName)
				return nil, connerr.PermissionDenied("user is not allowed to make this module public")
			}
		}
	}

	updated, err := s.moduleDBStorage.Update(ctx, in.Msg)
	if err != nil {
		s.logger.Error("failed to update module", "error", err, "procedure", "UpdateModule", "module", moduleFullName)
		return nil, connerr.FromDB(err)
	}

	return &connect.Response[registrypbv1.UpdateModuleResponse]{
		Msg: &registrypbv1.UpdateModuleResponse{Module: updated},
	}, nil
}

// DeleteModule permanently removes a module, its repository and its history.
//
// The repository is removed inside the database transaction, the one place a
// Gitaly call is made with a transaction open. Deletion has no compensation:
// a repository cannot be restored once removed. Ordered this way, a Gitaly
// failure rolls the rows back and the module is left whole, and a commit
// failure after the repository is gone leaves a row that a retry deletes,
// since DeleteRepository treats a missing repository as success. The other
// order, rows first, would leave an orphan repository that makes the name
// impossible to create again.
func (s *Server) DeleteModule(ctx context.Context, in *connect.Request[registrypbv1.DeleteModuleRequest]) (*connect.Response[registrypbv1.DeleteModuleResponse], error) {
	const procedure = "DeleteModule"
	user, ok := ctx.Value(constants.ContextKeyUser).(*identityv1.User)
	if !ok {
		s.logger.Error("missing user in context", "procedure", procedure)
		return nil, connerr.Unauthenticated("not authenticated")
	}

	moduleFullName := in.Msg.Owner + "/" + in.Msg.Name

	can, err := s.authorization.Can(ctx, &constants.Policy{
		Subject:      user.Username,
		ResourceType: string(constants.ResourceModule),
		Action:       string(constants.ActionDelete),
		Domain:       moduleFullName,
	})
	if err != nil {
		return nil, err
	}
	if !can.Allowed {
		s.logger.Warn("user not allowed to delete module", "procedure", procedure, "user_id", user.Id, "module", moduleFullName)
		return nil, connerr.PermissionDenied("user is not allowed to delete this module")
	}

	current, err := s.moduleDBStorage.GetModuleByOwnerAndName(ctx, in.Msg.Owner, in.Msg.Name)
	if err != nil {
		return nil, connerr.FromDB(err)
	}

	var logID uuid.UUID
	if s.gitalyOpLog != nil {
		logID, _ = s.gitalyOpLog.CreatePending(ctx, gitalyoplog.OpDeleteModule, moduleFullName, user.Id)
	}

	_, err = s.uow.Do(ctx, func(txCtx context.Context) (interface{}, error) {
		if err := s.moduleDBStorage.Delete(txCtx, current.Id); err != nil {
			return nil, connerr.FromDB(err)
		}
		if err := s.gitStorage.DeleteRepository(txCtx, current.Name); err != nil {
			return nil, err
		}
		return nil, nil
	}, 30*time.Second)
	if err != nil {
		s.logger.Error("failed to delete module", "error", err, "procedure", procedure, "module", moduleFullName)
		s.recordOpLogFailure(ctx, logID, err)
		return nil, err
	}
	if s.gitalyOpLog != nil && logID != uuid.Nil {
		_ = s.gitalyOpLog.UpdateStatus(ctx, logID, gitalyoplog.StatusCompleted, "", "")
	}

	s.logger.Info("module deleted", "procedure", procedure, "actor", user.Username, "module", moduleFullName)
	return connect.NewResponse(&registrypbv1.DeleteModuleResponse{}), nil
}

func lintPresetToRule(p registrypbv1.LintPreset) string {
	switch p {
	case registrypbv1.LintPreset_LINT_PRESET_BASIC:
		return "BASIC"
	case registrypbv1.LintPreset_LINT_PRESET_MINIMAL:
		return "MINIMAL"
	case registrypbv1.LintPreset_LINT_PRESET_COMMENTS:
		return "COMMENTS"
	default:
		return "DEFAULT"
	}
}
