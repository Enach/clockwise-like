package config

import (
	"regexp"
	"strings"
	"testing"
)

// PAC-121: the release pipeline builds the frontend image with
// VITE_SENTRY_DSN taken from a repository variable, so web monitoring can be
// turned on without a built-in DSN (PAC-86). Files are read from the
// repository root, like the other deployment-file tests in this package.

func normalizedLines(text string) []string {
	return strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
}

func TestFrontendDockerfileDeclaresSentryDSNBuildArg(t *testing.T) {
	lines := normalizedLines(readRepoFile(t, "docker/frontend.dockerfile"))
	argLine, envLine, buildLine, stageStart, stageEnd := -1, -1, -1, -1, len(lines)
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "FROM ") && strings.Contains(line, " AS node-builder"):
			stageStart = i
		case strings.HasPrefix(line, "FROM ") && stageStart >= 0 && i > stageStart && stageEnd == len(lines):
			stageEnd = i
		}
		if stageStart < 0 || i <= stageStart || stageEnd != len(lines) {
			continue
		}
		switch {
		case regexp.MustCompile(`^ARG VITE_SENTRY_DSN(=""|=)?$`).MatchString(line):
			argLine = i
		case regexp.MustCompile(`^ENV VITE_SENTRY_DSN=\$\{?VITE_SENTRY_DSN\}?$`).MatchString(line):
			envLine = i
		case strings.HasPrefix(line, "RUN ") && strings.Contains(line, "npm run build"):
			buildLine = i
			// Passed inline only counts when it forwards the build arg; a literal
			// value (or an empty one) would blank or hard-code the DSN.
			if regexp.MustCompile(`(^|\s)VITE_SENTRY_DSN=("?)\$\{?VITE_SENTRY_DSN\}?("?)(\s|$)`).MatchString(line) {
				envLine = i
			}
		}
	}
	if stageStart < 0 {
		t.Fatal("docker/frontend.dockerfile has no node-builder stage")
	}
	if buildLine < 0 {
		t.Fatal("the node-builder stage does not run npm run build")
	}
	if argLine < 0 || argLine > buildLine {
		t.Error(`the node-builder stage does not declare ARG VITE_SENTRY_DSN="" before npm run build`)
	}
	if envLine < 0 || envLine < argLine || envLine > buildLine {
		t.Error("VITE_SENTRY_DSN is not visible to npm run build (no ENV VITE_SENTRY_DSN=${VITE_SENTRY_DSN} between the ARG and the build, nor inline)")
	}
}

func TestReleaseWorkflowPassesSentryDSNFromRepositoryVariable(t *testing.T) {
	lines := normalizedLines(readRepoFile(t, ".github/workflows/release.yml"))
	// Find the build-push step whose file is docker/frontend.dockerfile: the
	// step spans from its "- name:"/"- uses:" line to the next step.
	fileLine := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "file: docker/frontend.dockerfile" {
			if fileLine >= 0 {
				t.Fatal("release.yml builds docker/frontend.dockerfile more than once")
			}
			fileLine = i
		}
	}
	if fileLine < 0 {
		t.Fatal("release.yml does not build docker/frontend.dockerfile")
	}
	isStepStart := regexp.MustCompile(`^\s*- (name|uses|run|id):`)
	start, end := fileLine, len(lines)
	for start > 0 && !isStepStart.MatchString(lines[start]) {
		start--
	}
	for i := fileLine + 1; i < len(lines); i++ {
		if isStepStart.MatchString(lines[i]) {
			end = i
			break
		}
	}
	step := strings.Join(lines[start:end], "\n")
	if !regexp.MustCompile(`(?m)^\s*context: \.$`).MatchString(step) {
		t.Error("the frontend image step does not build from context .")
	}
	if !regexp.MustCompile(`(?m)^\s*build-args: \|`).MatchString(step) {
		t.Error("the frontend image step has no build-args block")
	}
	if !regexp.MustCompile(`(?m)^\s*VITE_SENTRY_DSN=\$\{\{ vars\.VITE_SENTRY_DSN \}\}\s*$`).MatchString(step) {
		t.Error("the frontend image step does not pass VITE_SENTRY_DSN=${{ vars.VITE_SENTRY_DSN }}")
	}
	if strings.Contains(step, "secrets.VITE_SENTRY_DSN") {
		t.Error("VITE_SENTRY_DSN must come from a repository variable, not a secret: it ships in the public bundle")
	}
}

// PAC-86 guard extended to the files PAC-121 touches: no DSN value is
// committed; the DSN arrives only through the build arg.
func TestReleaseFilesShipNoSentryDSN(t *testing.T) {
	dsnValue := regexp.MustCompile(`https://[^\s"'@]+@[^\s"']*ingest[^\s"']*`)
	for _, f := range []string{"docker/frontend.dockerfile", ".github/workflows/release.yml"} {
		text := readRepoFile(t, f)
		if m := dsnValue.FindString(text); m != "" {
			t.Errorf("%s contains a Sentry DSN (%s); it must arrive only through the build arg", f, m)
		}
		if strings.Contains(text, "sentry.io") {
			t.Errorf("%s mentions sentry.io; no ingest host is committed", f)
		}
	}
}
