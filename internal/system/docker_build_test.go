package system

import (
	"strings"
	"testing"
)

func TestControlPoint534DockerBuild(t *testing.T) {
	t.Run("控制点 5.3.4 功能验证", func(t *testing.T) {
		spec, err := LoadDockerBuildSpecFromEnv(func(key string) string {
			switch key {
			case "DOCKER_BUILD_BUILDER_IMAGE":
				return "golang:1.25-alpine"
			case "DOCKER_BUILD_RUNTIME_IMAGE":
				return "alpine:3.21"
			case "DOCKER_BUILD_BINARY_NAME":
				return "ledgerbotd"
			case "DOCKER_BUILD_TARGET":
				return "./cmd/ledgerbot"
			case "DOCKER_BUILD_WORKDIR":
				return "/srv/app"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("加载 docker build 配置失败: %v", err)
		}
		if spec.RuntimeImage != "alpine:3.21" {
			t.Fatalf("runtime 镜像错误: want=%q got=%q", "alpine:3.21", spec.RuntimeImage)
		}
		if spec.BinaryName != "ledgerbotd" {
			t.Fatalf("二进制名称错误: want=%q got=%q", "ledgerbotd", spec.BinaryName)
		}

		dockerfile, err := BuildMinimalAlpineDockerfile(spec)
		if err != nil {
			t.Fatalf("生成 Dockerfile 失败: %v", err)
		}
		required := []string{
			"FROM golang:1.25-alpine AS builder",
			"RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags=\"-s -w\" -o /out/ledgerbotd ./cmd/ledgerbot",
			"FROM alpine:3.21",
			"RUN apk --no-cache add ca-certificates",
			"WORKDIR /srv/app",
			"COPY --from=builder /out/ledgerbotd /srv/app/ledgerbotd",
			"USER 65532:65532",
			"ENTRYPOINT [\"/srv/app/ledgerbotd\"]",
		}
		for _, fragment := range required {
			if !strings.Contains(dockerfile, fragment) {
				t.Fatalf("Dockerfile 缺少关键片段: %q\ndockerfile=%s", fragment, dockerfile)
			}
		}
	})
}

func TestLoadDockerBuildSpecFromEnvFallback(t *testing.T) {
	spec, err := LoadDockerBuildSpecFromEnv(func(key string) string {
		if key == "DOCKER_BUILD_RUNTIME_IMAGE" {
			return "ubuntu:24.04"
		}
		return ""
	})
	if err == nil {
		t.Fatalf("非法 runtime 镜像应返回回退告警")
	}
	if !strings.Contains(err.Error(), "DOCKER_BUILD_RUNTIME_IMAGE") {
		t.Fatalf("错误信息应包含非法环境变量名: %v", err)
	}
	if spec.RuntimeImage != DefaultDockerBuildRuntimeImage {
		t.Fatalf("非法 runtime 镜像应回退默认值: want=%q got=%q", DefaultDockerBuildRuntimeImage, spec.RuntimeImage)
	}
}

func TestNormalizeDockerBuildSpecRejectsNonAlpineRuntime(t *testing.T) {
	_, err := NormalizeDockerBuildSpec(DockerBuildSpec{RuntimeImage: "debian:bookworm"})
	if err == nil {
		t.Fatalf("非 alpine runtime 镜像应被拒绝")
	}
	if !strings.Contains(err.Error(), "alpine") {
		t.Fatalf("错误信息应包含 alpine 约束: %v", err)
	}
}
