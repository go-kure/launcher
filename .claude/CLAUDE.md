# Claude Instructions for Launcher

## Primary Reference

**Read `AGENTS.md` first** - it contains comprehensive instructions for working with this codebase, including:
- Repository structure
- Development workflow
- Code conventions
- Testing patterns

## Claude-Specific Notes

### Context Files

When working on Launcher, load these files for context:
- `AGENTS.md` - Agent instructions and development guide
- `DEVELOPMENT.md` - Development workflow documentation
- `go.mod` - Dependencies and module path

### Code Generation Patterns

When generating CLI commands or package handlers:

```go
// pkg/cmd/kurel/<command>.go
package kurel

// New<Command>Command creates the <command> cobra command
func New<Command>Command() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "<name>",
        Short: "<short description>",
    }
    return cmd
}
```

### Error Handling

Always use `github.com/go-kure/launcher/pkg/errors` in application code — never call `fmt.Errorf` directly outside of `pkg/errors` itself.

```go
import "github.com/go-kure/launcher/pkg/errors"

return errors.Wrap(err, "context about what failed")
return errors.Wrapf(err, "failed to process %s", name)
return errors.New("description of error")
return errors.Errorf("invalid value: %s", val)
```

### Commits

Follow conventional commits:
- `feat:` - New features
- `fix:` - Bug fixes
- `chore:` - Maintenance
- `build:` - Build system changes
- `test:` - Test additions/changes
- `docs:` - Documentation

### Git Workflow

`main` is protected — always create a feature branch before making changes:

```bash
git checkout -b <type>/<description> main
# make changes, commit
git push -u origin <type>/<description>
gh pr create
```

Required checks: `lint`, `test`, `build`, `rebase-check`. Branches are automatically rebased when main is updated. See `AGENTS.md` § Git Workflow for full details.

## Quick Commands

```bash
# Build all executables
mise run build
# or: make build

# Test
mise run test
# or: make test

# Lint
mise run lint
# or: make lint

# Tidy dependencies
mise run tidy
# or: make tidy

# Quick pre-commit check
mise run check
# or: make check

# Run all checks (tidy, lint, test)
mise run verify
# or: make precommit
```
