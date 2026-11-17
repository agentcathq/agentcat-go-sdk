# Contributing to AgentCat Go SDK

Thank you for your interest in contributing to the AgentCat Go SDK! This document provides guidelines and instructions for contributing to this project.

## Getting Started

### Prerequisites

- **Go 1.25 or later** - The adapter modules declare `go 1.25` (mcpgo pins `1.25.5`)
- **Git** - For version control
- A GitHub account for submitting pull requests

### Setting Up Your Development Environment

1. Fork the repository on GitHub
2. Clone your fork locally:
   ```bash
   git clone https://github.com/YOUR_USERNAME/agentcat-go-sdk.git
   cd agentcat-go-sdk
   ```
3. Add the upstream repository:
   ```bash
   git remote add upstream https://github.com/agentcathq/agentcat-go-sdk.git
   ```
4. Install dependencies:
   ```bash
   go mod download
   ```

## Development Workflow

### Running Tests

Before submitting a pull request, ensure all tests pass:

```bash
go test ./...
```

To run tests with race detection and coverage:

```bash
go test -race -coverprofile=coverage.out ./...
```

View coverage report:

```bash
go tool cover -html=coverage.out
```

### Code Formatting

This project uses standard Go formatting. Format your code before committing:

```bash
go fmt ./...
```

**Important:** All code must be properly formatted. The CI pipeline will fail if any files are not formatted.

### Code Quality

Ensure your code passes basic checks:

```bash
go vet ./...
```

Update dependencies if needed:

```bash
go mod tidy
```

### Building

Verify that your changes build successfully:

```bash
go build ./...
```

## Submitting Changes

### Pull Request Process

1. **Create a branch** for your changes:
   ```bash
   git checkout -b feature/your-feature-name
   ```

2. **Make your changes** and commit them with clear, descriptive commit messages

3. **Push to your fork**:
   ```bash
   git push origin feature/your-feature-name
   ```

4. **Open a Pull Request** against the `main` branch

5. **Ensure CI passes** - The automated tests and formatting checks must pass

### Pull Request Requirements

- [ ] All tests pass (`go test ./...`)
- [ ] Code is properly formatted (`go fmt ./...`)
- [ ] New code includes appropriate tests
- [ ] Documentation is updated if needed
- [ ] A user-facing change has its entry under `[Unreleased]` in `CHANGELOG.md`
- [ ] Commit messages are clear and descriptive

## Code Guidelines

### Testing

- Write tests for all new functionality
- Maintain or improve test coverage
- Use table-driven tests where appropriate
- Follow existing test patterns in the codebase

### Code Style

- Follow standard Go conventions and idioms
- Keep functions focused and concise
- Use meaningful variable and function names
- Add comments for exported functions and types
- Use `gofmt` for formatting (standard Go formatting)

### Error Handling

- Return errors rather than panicking
- Provide context in error messages
- Use `fmt.Errorf` with `%w` for error wrapping when appropriate

## Release a version

The release workflow (`.github/workflows/release.yml`) does the release in two steps, with a pull
request between them. The Java, Rust and Ruby SDKs release the same way.

Three modules ship together and must carry the **same version**: the root module
`go.agentcat.com/sdk/v2` (tag `vX.Y.Z`), `go.agentcat.com/sdk/mcpgo/v2` (tag `mcpgo/vX.Y.Z`) and
`go.agentcat.com/sdk/officialsdk/v2` (tag `officialsdk/vX.Y.Z`). Each adapter's `go.mod` requires
the root module by that exact version. The `replace` directives make local builds ignore it, so a
mismatch is invisible here and only breaks for someone who installs from the proxy. A Go module
has no registry upload: a version is a set of git tags, and the Go module proxy reads them.

1. In GitHub, open **Actions > Release > Run workflow** on `main`. Keep `patch`, or type `minor`
   or an exact version such as `2.2.0`. The words count from the newest `vX.Y.Z` tag of the root
   module. `major` stops the workflow: a new major version needs new module paths (`/v3`), so it
   stays a manual task.
2. The workflow opens the pull request "Release vX.Y.Z". It sets the version in every place that
   holds it: the root require of `mcpgo/go.mod` and `officialsdk/go.mod`, the adapter require and
   the indirect root require of the five `examples/*/*/go.mod`, and any `@vX.Y.Z` install command
   in `README.md` and `MIGRATION.md`. It turns `[Unreleased]` into the dated section of the
   version. It also starts CI and the two compatibility workflows on the pull request: they hold
   the required status checks of `main`. These stop the workflow: places that do not agree on one
   version, an `[Unreleased]` section with no entry, a tag of the version that exists already, or
   a version that the Go module proxy already has.
3. Read the pull request, and merge it when the checks pass.
4. The merge starts the workflow again. It waits for CI and the two compatibility workflows on
   the merge commit. Then it tags that commit three times, the root tag first, so that the
   adapters have something to resolve against. It waits until the Go module proxy serves both
   adapters at the new version, from a directory outside this repository where the `replace`
   directives do not apply. Then it creates the GitHub release `vX.Y.Z`, with the CHANGELOG section
   as the notes.

If a step fails, fix the cause and re-run the failed jobs of that run. A tag that is already on the
merge commit is a skip, so a re-run creates only the tags that are missing. A tag on another commit
stops the workflow: the workflow never moves or deletes a tag. The GitHub release comes last, and a
re-run skips a release that exists.

Tags are immutable on the module proxy: a bad tag needs a new version, not a force-push. Do not
push a `vX.Y.Z` tag of a version that you do not release: the proxy keeps it for good.

Each pull request with a user-facing change adds its entry under `[Unreleased]` in `CHANGELOG.md`.
The release takes the entries from there.

To run a step of the workflow by hand, run `go run ./scripts/release prepare <patch | minor | major |
X.Y.Z> <YYYY-MM-DD>`, `status` or `notes <X.Y.Z>` from the root of the repository.
`scripts/release/release_test.go` tests these steps in the normal `go test ./...` of the root module.

The workflow supports only versions `X.Y.Z`. A pre-release (for example `v2.2.0-beta.1`) stays a
manual task: set the version in every place above, merge, then tag the merge commit by hand, the
root tag first, and check the result from outside the repository:

```bash
git tag vX.Y.Z-beta.1
git tag mcpgo/vX.Y.Z-beta.1
git tag officialsdk/vX.Y.Z-beta.1
git push origin vX.Y.Z-beta.1 mcpgo/vX.Y.Z-beta.1 officialsdk/vX.Y.Z-beta.1

cd "$(mktemp -d)" && go mod init check
go get go.agentcat.com/sdk/mcpgo/v2@vX.Y.Z-beta.1
go get go.agentcat.com/sdk/officialsdk/v2@vX.Y.Z-beta.1
```

`@latest` never selects a pre-release, so a beta does not become the default install; ask for it
explicitly. A by-hand pre-release does not change the next version of the workflow: the words count
only from `vX.Y.Z` tags. The release steps accept only `X.Y.Z` in the places that hold the
version, so set them back to `X.Y.Z` (the version that you plan to release) before you run the
workflow again.

### One-time setup

The release needs no publishing secret and no registry account: `GITHUB_TOKEN` creates the tags,
and the Go module proxy fetches them from this public repository.

- In the repository settings, turn on **Actions > General > Allow GitHub Actions to create and
  approve pull requests**, so that the workflow can open the release pull request.
- Create a `release` environment, limited to the `main` branch.

## Additional Resources

For more detailed technical information about the project structure, internal architecture, and advanced development guidelines, see [AGENTS.md](AGENTS.md).

## Questions?

If you have questions or need help:
- Open an issue for discussion
- Check existing issues and pull requests
- Review the [README.md](README.md) for usage documentation

## License

By contributing to this project, you agree that your contributions will be licensed under the MIT License.
