package system

import (
	"fmt"
	"os"
	"path"
	"strings"
)

const (
	DefaultDockerBuildBuilderImage = "golang:1.25-alpine"
	DefaultDockerBuildRuntimeImage = "alpine:3.20"
	DefaultDockerBuildBinaryName   = "ledgerbot"
	DefaultDockerBuildTarget       = "./cmd/ledgerbot"
	DefaultDockerBuildWorkDir      = "/app"
)

type DockerBuildSpec struct {
	BuilderImage string
	RuntimeImage string
	BinaryName   string
	BuildTarget  string
	WorkDir      string
}

func DefaultDockerBuildSpec() DockerBuildSpec {
	return DockerBuildSpec{
		BuilderImage: DefaultDockerBuildBuilderImage,
		RuntimeImage: DefaultDockerBuildRuntimeImage,
		BinaryName:   DefaultDockerBuildBinaryName,
		BuildTarget:  DefaultDockerBuildTarget,
		WorkDir:      DefaultDockerBuildWorkDir,
	}
}

func NormalizeDockerBuildSpec(spec DockerBuildSpec) (DockerBuildSpec, error) {
	normalized := DefaultDockerBuildSpec()

	if builderImage := strings.TrimSpace(spec.BuilderImage); builderImage != "" {
		normalized.BuilderImage = builderImage
	}
	if runtimeImage := strings.TrimSpace(spec.RuntimeImage); runtimeImage != "" {
		normalized.RuntimeImage = runtimeImage
	}
	if binaryName := strings.TrimSpace(spec.BinaryName); binaryName != "" {
		normalized.BinaryName = binaryName
	}
	if buildTarget := strings.TrimSpace(spec.BuildTarget); buildTarget != "" {
		normalized.BuildTarget = buildTarget
	}
	if workDir := strings.TrimSpace(spec.WorkDir); workDir != "" {
		normalized.WorkDir = path.Clean(workDir)
	}

	if strings.TrimSpace(normalized.BuilderImage) == "" {
		return DockerBuildSpec{}, fmt.Errorf("builder image must not be empty")
	}
	if !isAlpineImage(normalized.RuntimeImage) {
		return DockerBuildSpec{}, fmt.Errorf("runtime image must be alpine based")
	}
	if strings.TrimSpace(normalized.BinaryName) == "" {
		return DockerBuildSpec{}, fmt.Errorf("binary name must not be empty")
	}
	if strings.Contains(normalized.BinaryName, "/") || strings.Contains(normalized.BinaryName, "\\") {
		return DockerBuildSpec{}, fmt.Errorf("binary name must not contain path separator")
	}
	if strings.TrimSpace(normalized.BuildTarget) == "" {
		return DockerBuildSpec{}, fmt.Errorf("build target must not be empty")
	}
	if !strings.HasPrefix(normalized.WorkDir, "/") {
		return DockerBuildSpec{}, fmt.Errorf("work dir must be an absolute linux path")
	}

	return normalized, nil
}

func LoadDockerBuildSpecFromEnv(getenv func(string) string) (DockerBuildSpec, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	runtimeImageRaw := strings.TrimSpace(getenv("DOCKER_BUILD_RUNTIME_IMAGE"))
	runtimeImage, runtimeImageErr := parseDockerBuildRuntimeImage(runtimeImageRaw)
	binaryNameRaw := strings.TrimSpace(getenv("DOCKER_BUILD_BINARY_NAME"))
	binaryName, binaryNameErr := parseDockerBuildBinaryName(binaryNameRaw)
	workDirRaw := strings.TrimSpace(getenv("DOCKER_BUILD_WORKDIR"))
	workDir, workDirErr := parseDockerBuildWorkDir(workDirRaw)

	spec := DockerBuildSpec{
		BuilderImage: strings.TrimSpace(getenv("DOCKER_BUILD_BUILDER_IMAGE")),
		BuildTarget:  strings.TrimSpace(getenv("DOCKER_BUILD_TARGET")),
	}
	if runtimeImage != "" {
		spec.RuntimeImage = runtimeImage
	}
	if binaryName != "" {
		spec.BinaryName = binaryName
	}
	if workDir != "" {
		spec.WorkDir = workDir
	}

	normalized, err := NormalizeDockerBuildSpec(spec)
	if err != nil {
		fallback, fallbackErr := NormalizeDockerBuildSpec(DockerBuildSpec{})
		if fallbackErr != nil {
			return DockerBuildSpec{}, fmt.Errorf("normalize docker build defaults failed: %w", fallbackErr)
		}
		return fallback, fmt.Errorf("docker build config invalid, fallback to defaults: %w", err)
	}
	if runtimeImageErr != nil {
		return normalized, fmt.Errorf(
			"DOCKER_BUILD_RUNTIME_IMAGE=%q invalid, fallback to %s: %w",
			runtimeImageRaw,
			normalized.RuntimeImage,
			runtimeImageErr,
		)
	}
	if binaryNameErr != nil {
		return normalized, fmt.Errorf(
			"DOCKER_BUILD_BINARY_NAME=%q invalid, fallback to %s: %w",
			binaryNameRaw,
			normalized.BinaryName,
			binaryNameErr,
		)
	}
	if workDirErr != nil {
		return normalized, fmt.Errorf(
			"DOCKER_BUILD_WORKDIR=%q invalid, fallback to %s: %w",
			workDirRaw,
			normalized.WorkDir,
			workDirErr,
		)
	}
	return normalized, nil
}

func BuildMinimalAlpineDockerfile(spec DockerBuildSpec) (string, error) {
	normalized, err := NormalizeDockerBuildSpec(spec)
	if err != nil {
		return "", err
	}

	entrypointPath := path.Join(normalized.WorkDir, normalized.BinaryName)
	dockerfile := fmt.Sprintf(`FROM %s AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/%s %s

FROM %s
RUN apk --no-cache add ca-certificates
WORKDIR %s
COPY --from=builder /out/%s %s
USER 65532:65532
ENTRYPOINT ["%s"]
`,
		normalized.BuilderImage,
		normalized.BinaryName,
		normalized.BuildTarget,
		normalized.RuntimeImage,
		normalized.WorkDir,
		normalized.BinaryName,
		entrypointPath,
		entrypointPath,
	)
	return dockerfile, nil
}

func DockerBuildEntrypoint(spec DockerBuildSpec) (string, error) {
	normalized, err := NormalizeDockerBuildSpec(spec)
	if err != nil {
		return "", err
	}
	return path.Join(normalized.WorkDir, normalized.BinaryName), nil
}

func parseDockerBuildRuntimeImage(raw string) (string, error) {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return "", nil
	}
	if !isAlpineImage(normalized) {
		return "", fmt.Errorf("runtime image must be alpine based")
	}
	return normalized, nil
}

func parseDockerBuildBinaryName(raw string) (string, error) {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return "", nil
	}
	if strings.Contains(normalized, "/") || strings.Contains(normalized, "\\") {
		return "", fmt.Errorf("binary name must not contain path separator")
	}
	return normalized, nil
}

func parseDockerBuildWorkDir(raw string) (string, error) {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return "", nil
	}
	if !strings.HasPrefix(normalized, "/") {
		return "", fmt.Errorf("work dir must be an absolute linux path")
	}
	return path.Clean(normalized), nil
}

func isAlpineImage(image string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(image)), "alpine")
}
