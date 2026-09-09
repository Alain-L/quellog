# Contributing to _quellog_

Thank you for considering contributing to **quellog**! Contributions that improve
functionality, documentation, performance, or developer experience are highly
appreciated. Below are some guidelines to help you get started.

## Code of Conduct

Please review our [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) to understand the
expected behavior within our community.

## How to Contribute

**quellog** tries to balance pragmatism and good engineering practices. The
goal isn’t perfection, but consistency: keep the code readable, write clear
commits, add tests when it makes sense, and update the docs if behavior
changes.

### 1. Fork the Repository  
Create your own fork of the project on GitHub.

### 2. Create a Feature Branch  
Use a **descriptive** branch name with a conventional prefix, such as:  
- `feat/improve-parsing`  
- `fix/date-filter`  
- `docs/update-installation`  

### 3. Write Your Code
- Keep the code consistent with what’s already there.
- Add tests when relevant.
- Add comments or documentation where it helps understanding.

### 4. Commit Your Changes  
- Write **clear** and **concise** commit messages.  
- Follow [Conventional Commits](https://www.conventionalcommits.org/) (e.g. `fix(analysis): …`, `docs(changelog): …`).  

### 5. Submit a Pull Request  
- Open a pull request (PR) to the main repository.  
- Provide a comprehensive description of your changes, their necessity, and
  reference any relevant issues.  

## Reporting Issues

If you encounter a bug or have a feature request, please open an issue on
GitHub. Include as much detail as possible:
- Steps to reproduce  
- Expected vs. actual behavior  
- Environment details (OS, Go version, etc.)  

## Additional Notes

- **Building & testing:**  
  The embedded web assets are generated and gitignored, so generate them once
  before building or testing (otherwise the `web` package fails to compile):  
  ```sh
  go generate ./web/...   # or: make build
  go test ./...
  ```
- **Match the CI gates locally** before pushing — CI blocks on all of:  
  ```sh
  gofmt -s -l .        # formatting (must be empty)
  go vet ./...
  staticcheck ./...
  go test -race ./... -cover
  ```
  CI also builds the TinyGo WASM module and runs the web JS tests
  (`npm run test:unit` / `npm run test:contracts`).
- **Documentation:**  
  If your changes impact usage, update the documentation accordingly.  

Thank you for helping make _quellog_ a better project!