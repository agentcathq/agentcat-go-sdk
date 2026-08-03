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

## Releasing

Three modules ship together and must carry the **same version**, because each
adapter's `go.mod` requires the root module by that exact version. The
`replace` directives make local builds ignore it, so a mismatch is invisible
here and only breaks for someone installing from the proxy.

1. Set the version everywhere it appears, before tagging:
   - `mcpgo/go.mod` and `officialsdk/go.mod` — `require go.agentcat.com/sdk/v2 <version>`
   - the five `examples/*/*/go.mod` — the adapter require plus the indirect root require
   - install commands in `README.md` and `MIGRATION.md`
2. Merge to `main`, then tag that commit three times — the root tag first, so
   the adapters have something to resolve against:

   ```bash
   git tag v2.0.0-beta.1
   git tag mcpgo/v2.0.0-beta.1
   git tag officialsdk/v2.0.0-beta.1
   git push origin v2.0.0-beta.1 mcpgo/v2.0.0-beta.1 officialsdk/v2.0.0-beta.1
   ```

3. Verify from a directory outside this repo, where the `replace` directives do
   not apply — this is the only check that proves the published requires resolve:

   ```bash
   cd "$(mktemp -d)" && go mod init check
   go get go.agentcat.com/sdk/mcpgo/v2@v2.0.0-beta.1
   go get go.agentcat.com/sdk/officialsdk/v2@v2.0.0-beta.1
   ```

Prerelease versions are never selected by `@latest`, so a beta does not become
the default install; ask for it explicitly. Tags are immutable on the module
proxy — a bad tag needs a new version, not a force-push.

## Additional Resources

For more detailed technical information about the project structure, internal architecture, and advanced development guidelines, see [AGENTS.md](AGENTS.md).

## Questions?

If you have questions or need help:
- Open an issue for discussion
- Check existing issues and pull requests
- Review the [README.md](README.md) for usage documentation

## License

By contributing to this project, you agree that your contributions will be licensed under the MIT License.
