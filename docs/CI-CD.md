# CI/CD Pipeline

This project uses GitHub Actions for continuous integration and deployment.

## Workflows

### CI (`.github/workflows/ci.yml`)

Runs on every push to `main`/`master` and pull requests.

**Jobs:**

1. **Lint** - Runs golangci-lint on the codebase
2. **Build** - Builds all 4 services (bot, parser, userbot, api)
3. **Docker** - Builds Docker images (on push to main/master only)

### Deploy (`.github/workflows/deploy.yml`)

Runs on push to `main`/`master` and on git tags (v*).

**Jobs:**

1. **Build and Push** - Builds and pushes Docker images to GitHub Container Registry (GHCR)

Images are tagged with:
- Branch name + service name (e.g., `main-bot`)
- Semantic version + service (e.g., `v1.0.0-bot`)
- Commit SHA + service

## Local Testing

### Build all services locally:

```bash
go build ./cmd/bot
go build ./cmd/parser
go build ./cmd/userbot
go build ./cmd/api
```

### Run linter:

```bash
golangci-lint run ./...
```

## Registry

Docker images are pushed to GitHub Container Registry (GHCR):

```
ghcr.io/[username]/rss-reader:main-bot
ghcr.io/[username]/rss-reader:main-parser
ghcr.io/[username]/rss-reader:main-userbot
ghcr.io/[username]/rss-reader:main-api
```

To use images from GHCR, authenticate with your GitHub token:

```bash
echo $GITHUB_TOKEN | docker login ghcr.io -u $GITHUB_USERNAME --password-stdin
```

## Setup

1. **Repository secrets** - No additional secrets required for GHCR (uses `GITHUB_TOKEN`)
2. **Branch protection** - Consider enabling "Require status checks to pass before merging"
3. **Tags** - Semantic versioning tags (v1.0.0) will trigger releases

## Configuration

To customize the pipeline:

- Edit `.github/workflows/ci.yml` for CI rules
- Edit `.github/workflows/deploy.yml` for deployment rules
- Change registry/target in env variables

## Adding Docker Hub

To push to Docker Hub instead of (or in addition to) GHCR:

1. Add `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN` secrets to the repository
2. Add login step and update tags in `deploy.yml`

Example:

```yaml
- name: Log in to Docker Hub
  uses: docker/login-action@v2
  with:
    username: ${{ secrets.DOCKERHUB_USERNAME }}
    password: ${{ secrets.DOCKERHUB_TOKEN }}
```
