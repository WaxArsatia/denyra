package media_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/waxarsatia/denyra/internal/pipeline/adapters/media"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestMetaFLACApplySelectedPreservesPicturesAndUnselectedTags(t *testing.T) {
	root := t.TempDir()
	script, arguments := filepath.Join(root, "metaflac"), filepath.Join(root, "arguments")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DENYRA_TEST_ARGUMENTS\"\n"), 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DENYRA_TEST_ARGUMENTS", arguments)
	fields := []string{"TITLE", "ARTIST", "ALBUM", "ALBUMARTIST", "TRACKNUMBER", "TRACKTOTAL", "DISCNUMBER", "DISCTOTAL"}
	tags := domain.TagSet{"TITLE": {"New"}, "ARTIST": {"Artist"}, "ALBUM": {"Album"}, "ALBUMARTIST": {"Artist"}, "TRACKNUMBER": {"1"}, "TRACKTOTAL": {"1"}, "DISCNUMBER": {"1"}, "DISCTOTAL": {"1"}, "ISRC": {"KEEP"}, "CUSTOM": {"KEEP"}}
	evidence, err := (media.MetaFLAC{Binary: script, Version: "test", Runner: media.Runner{}}).ApplySelected(context.Background(), "/tmp/track.flac", tags, fields, true)
	if err != nil || len(evidence) != 1 {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
	body, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	args := string(body)
	for _, field := range fields {
		if !strings.Contains(args, "--remove-tag="+field+"\n") || !strings.Contains(args, "--set-tag="+field+"=") {
			t.Fatalf("selected field %s missing from args:\n%s", field, args)
		}
	}
	for _, forbidden := range []string{"--remove-tag=ISRC", "--set-tag=ISRC", "CUSTOM", "block-type=PICTURE"} {
		if strings.Contains(args, forbidden) {
			t.Fatalf("unselected data changed by %q:\n%s", forbidden, args)
		}
	}
}
