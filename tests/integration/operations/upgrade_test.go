package operations_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type protectedFile struct {
	path    string
	content []byte
	mode    os.FileMode
}

func TestForwardUpdatePreCutoverFailuresPreserveRelease(t *testing.T) {
	for _, failure := range []string{"dirty", "fetch", "merge", "render", "pull", "build"} {
		t.Run(failure, func(t *testing.T) {
			f := newManagementFixture(t)
			before := prepareForwardUpdateFixture(t, f, false)
			out, err := f.updateCommand(failure).CombinedOutput()
			if err == nil {
				t.Fatalf("%s failure succeeded: %s", failure, out)
			}
			if strings.Contains(f.log(), " up -d --remove-orphans") {
				t.Fatalf("pre-cutover failure started candidate stack:\n%s", f.log())
			}
			assertReleaseCommit(t, f, strings.Repeat("a", 40))
			assertProtectedTrees(t, before)
			for _, field := range []string{"phase=", "affected=", "deployed_commit=", "retry=./denyra update"} {
				if !strings.Contains(string(out), field) {
					t.Errorf("failure output missing %q: %s", field, out)
				}
			}
		})
	}
}

func TestForwardUpdatePostCutoverFailureKeepsCandidate(t *testing.T) {
	for _, failure := range []string{"start", "smoke"} {
		t.Run(failure, func(t *testing.T) {
			f := newManagementFixture(t)
			before := prepareForwardUpdateFixture(t, f, false)
			out, err := f.updateCommand(failure).CombinedOutput()
			if err == nil {
				t.Fatalf("%s failure succeeded: %s", failure, out)
			}
			assertReleaseCommit(t, f, strings.Repeat("b", 40))
			assertProtectedTrees(t, before)
			log := f.log()
			for _, forbidden := range []string{"prior-compose.yaml", "prior-images.yaml", " rollback", " restore"} {
				if strings.Contains(log, forbidden) {
					t.Fatalf("post-cutover failure used retired recovery %q:\n%s", forbidden, log)
				}
			}
			for _, field := range []string{"phase=", "deployed_commit=" + strings.Repeat("b", 40), "logs=./denyra logs", "retry=./denyra update"} {
				if !strings.Contains(string(out), field) {
					t.Errorf("failure output missing %q: %s", field, out)
				}
			}
		})
	}
}

func TestInterruptedUpdateConvergesForward(t *testing.T) {
	f := newManagementFixture(t)
	before := prepareForwardUpdateFixture(t, f, false)
	marker := filepath.Join(f.home, "start-failed-once")
	first := f.updateCommand("start-once")
	first.Env = append(first.Env, "DENYRA_TEST_START_MARKER="+marker)
	if out, err := first.CombinedOutput(); err == nil {
		t.Fatalf("interrupted cutover succeeded: %s", out)
	}
	assertReleaseCommit(t, f, strings.Repeat("b", 40))
	assertProtectedTrees(t, before)

	if err := os.WriteFile(f.logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	retry := f.updateCommand("unhealthy")
	retry.Env = append(retry.Env, "DENYRA_TEST_START_MARKER="+marker)
	out, err := retry.CombinedOutput()
	if err != nil || strings.Contains(string(out), "already current") {
		t.Fatalf("retry did not converge err=%v output=%q", err, out)
	}
	if !strings.Contains(f.log(), " up -d --remove-orphans") {
		t.Fatalf("retry did not reconcile candidate stack:\n%s", f.log())
	}
	assertProtectedTrees(t, before)
}

func TestForwardUpdateEqualCommitReconcilesUnhealthyStack(t *testing.T) {
	f := newManagementFixture(t)
	before := prepareForwardUpdateFixture(t, f, true)
	out, err := f.updateCommand("unhealthy").CombinedOutput()
	if err != nil {
		t.Fatalf("equal-commit reconciliation failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "already current") {
		t.Fatalf("unhealthy deployment treated as current: %s", out)
	}
	if !strings.Contains(f.log(), " up -d --remove-orphans") {
		t.Fatalf("equal commit did not reconcile stack:\n%s", f.log())
	}
	assertProtectedTrees(t, before)
}

func TestForwardUpdatePullsOnlyExternalImages(t *testing.T) {
	f := newManagementFixture(t)
	prepareForwardUpdateFixture(t, f, false)
	if out, err := f.updateCommand("").CombinedOutput(); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	log := f.log()
	if !strings.Contains(log, " pull --policy always slskd sftpgo") {
		t.Fatalf("external image pull missing:\n%s", log)
	}
	for _, localImage := range []string{"navidrome", "acquisition-gateway", "media-pipeline", "lidarr", "restic"} {
		if strings.Contains(log, " pull --policy always slskd sftpgo "+localImage) {
			t.Fatalf("locally built or retired image %q was pulled:\n%s", localImage, log)
		}
	}
	if !strings.Contains(log, " build --pull") {
		t.Fatalf("local image build missing:\n%s", log)
	}
}

func TestImageCleanupRemovesOnlyUnreferencedDenyraImages(t *testing.T) {
	f := newManagementFixture(t)
	prepareForwardUpdateFixture(t, f, false)
	out, err := f.updateCommand("image-cleanup").CombinedOutput()
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	log := f.log()
	if !strings.Contains(log, "image rm sha256:old-denyra") {
		t.Fatalf("unreferenced Denyra image retained:\n%s", log)
	}
	for _, forbidden := range []string{"image rm sha256:running-denyra", "image rm sha256:external", "image rm --force", "image prune", "system prune"} {
		if strings.Contains(log, forbidden) {
			t.Fatalf("unsafe image cleanup %q:\n%s", forbidden, log)
		}
	}
}

func prepareForwardUpdateFixture(t *testing.T, f *managementFixture, equalHead bool) map[string]protectedFile {
	t.Helper()
	for _, path := range []string{filepath.Join(f.home, "config"), filepath.Join(f.home, "secrets")} {
		if err := os.MkdirAll(path, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	deployed := strings.Repeat("a", 40)
	if equalHead {
		deployed = strings.Repeat("b", 40)
	}
	env := "DENYRA_HOME=" + f.home + "\nDENYRA_CONFIG_DIR=" + filepath.Join(f.home, "config") +
		"\nDENYRA_SECRETS_DIR=" + filepath.Join(f.home, "secrets") + "\nDENYRA_DATA_ROOT=" + filepath.Join(f.home, "data") +
		"\nDENYRA_IMAGE_TAG=" + deployed[:12] + "\nDENYRA_GIT_COMMIT=" + deployed + "\nDENYRA_RELEASE_REFRESH=fixture\n"
	if err := os.WriteFile(filepath.Join(f.home, "config", "denyra.env"), []byte(env), 0o640); err != nil {
		t.Fatal(err)
	}

	paths := []string{
		"library/managed.flac", "library-unmanaged/unmanaged.flac", "state/gateway/denyra.db",
		"state/pipeline/denyra.db", "state/lidarr/config.xml", "state/slskd/slskd.db",
		"state/sftpgo/sftpgo.db", "state/navidrome/navidrome.db", "incoming/uploading/session.part",
		"processing/work/candidate/track.flac", "processing/approved/candidate/track.flac",
		"quarantine/candidate/track.flac", "downloads/slskd/complete-id/track.flac",
		"downloads/spotiflac/job-id/track.flac",
	}
	before := make(map[string]protectedFile, len(paths))
	for index, relative := range paths {
		path := filepath.Join(f.home, "data", filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		content := []byte(relative + " sentinel\n")
		mode := os.FileMode(0o600 + index%2*0o40)
		if err := os.WriteFile(path, content, mode); err != nil {
			t.Fatal(err)
		}
		before[relative] = protectedFile{path: path, content: content, mode: mode}
	}

	f.writeExecutable("git", `#!/bin/sh
printf 'git %s\n' "$*" >> "$DENYRA_TEST_LOG"
case "$*" in
  "diff --quiet --ignore-submodules --") [ "${DENYRA_TEST_UPDATE_FAILURE:-}" != dirty ] ;;
  "diff --cached --quiet --ignore-submodules --") exit 0 ;;
  "symbolic-ref --quiet --short HEAD") echo main ;;
  "fetch origin main") [ "${DENYRA_TEST_UPDATE_FAILURE:-}" != fetch ] ;;
  "merge --ff-only origin/main") [ "${DENYRA_TEST_UPDATE_FAILURE:-}" != merge ] ;;
  "rev-parse HEAD") printf '%040d\n' 0 | tr 0 b ;;
  "rev-parse --show-toplevel") printf '%s\n' "$DENYRA_TEST_REPO" ;;
  *) exit 0 ;;
esac
`)
	f.writeExecutable("docker", `#!/bin/sh
printf '%s\n' "$*" >> "$DENYRA_TEST_LOG"
case "$*" in
  "compose version"*) exit 0 ;;
  *" config"*|*" config --quiet"*) [ "${DENYRA_TEST_UPDATE_FAILURE:-}" != render ] ;;
  *" pull --policy always slskd sftpgo"*) [ "${DENYRA_TEST_UPDATE_FAILURE:-}" != pull ] ;;
  *" pull --policy always slskd sftpgo restic"*) [ "${DENYRA_TEST_UPDATE_FAILURE:-}" != pull ] ;;
  *" build --pull"*) [ "${DENYRA_TEST_UPDATE_FAILURE:-}" != build ] ;;
  *" ps -q "*) for service do :; done; echo "cid-$service" ;;
  "inspect --format {{.Image}} running-container") printf 'sha256:running-denyra\n' ;;
  "inspect --format {{.Image}} "*) echo "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" ;;
  *" ps --format json"*)
    [ "${DENYRA_TEST_UPDATE_FAILURE:-}" != unhealthy ] && printf '[{"Service":"acquisition-gateway","Health":"healthy","State":"running"}]\n'
    ;;
  *" up -d --remove-orphans --wait --wait-timeout "*)
    if [ "${DENYRA_TEST_UPDATE_FAILURE:-}" = start ]; then exit 31; fi
    if [ "${DENYRA_TEST_UPDATE_FAILURE:-}" = start-once ] && [ ! -e "$DENYRA_TEST_START_MARKER" ]; then
      : > "$DENYRA_TEST_START_MARKER"; exit 32
    fi
    ;;
  *" exec -T acquisition-gateway "*) [ "${DENYRA_TEST_UPDATE_FAILURE:-}" != smoke ] ;;
  *" exec -T "*) exit 0 ;;
  "image ls --filter label=io.denyra.project=denyra --quiet")
    [ "${DENYRA_TEST_UPDATE_FAILURE:-}" = image-cleanup ] && printf 'sha256:old-denyra\nsha256:running-denyra\n'
    ;;
  "ps --quiet") [ "${DENYRA_TEST_UPDATE_FAILURE:-}" = image-cleanup ] && printf 'running-container\n' ;;
  "image rm "*) exit 0 ;;
  "inspect "*) exit 0 ;;
  *) exit 0 ;;
esac
`)
	return before
}

func (f *managementFixture) updateCommand(failure string) *exec.Cmd {
	f.t.Helper()
	cmd := f.command("update")
	cmd.Env = append(cmd.Env,
		"DENYRA_DATA_ROOT="+filepath.Join(f.home, "data"),
		"DENYRA_WAIT_SECONDS=1", "DENYRA_TEST_UPDATE_FAILURE="+failure, "DENYRA_TEST_REPO="+f.repo,
	)
	return cmd
}

func assertReleaseCommit(t *testing.T, f *managementFixture, want string) {
	t.Helper()
	env, err := os.ReadFile(filepath.Join(f.home, "config", "denyra.env"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(env), "DENYRA_GIT_COMMIT=") != 1 || !strings.Contains(string(env), "DENYRA_GIT_COMMIT="+want+"\n") {
		t.Fatalf("release commit is not %s:\n%s", want, env)
	}
}

func assertProtectedTrees(t *testing.T, before map[string]protectedFile) {
	t.Helper()
	for relative, sentinel := range before {
		content, err := os.ReadFile(sentinel.path)
		if err != nil {
			t.Errorf("protected path %s missing: %v", relative, err)
			continue
		}
		if string(content) != string(sentinel.content) {
			t.Errorf("protected path %s changed", relative)
		}
		info, err := os.Stat(sentinel.path)
		if err != nil || info.Mode().Perm() != sentinel.mode.Perm() {
			t.Errorf("protected path %s mode=%v err=%v want=%v", relative, infoMode(info), err, sentinel.mode.Perm())
		}
	}
}

func infoMode(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode().Perm()
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
