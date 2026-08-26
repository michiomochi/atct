package store

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

var migratedDocumentationPrefixes = []string{
	"007c3e78", "015c9b1a", "05f6fa0e", "06435e48", "07f548e4",
	"0b79b6a3", "0e3eb9d2", "0f1dbafa", "0fca9e51", "0fe78eaf",
	"141d71dc", "1dc54896", "1e082f2f", "1f144613", "20007786",
	"2a5880cc", "2fd85a78", "340866f0", "36d5332e", "3846c275",
	"38a79d54", "39fa8e55", "3fc12de9", "40997151", "41606dfb",
	"419e0dff", "44433505", "446d87f0", "44ae6e4e", "49ee01d8",
	"4bd9f4de", "4fa268b2", "50b1fe60", "53fda2ab", "577e8da5",
	"58ae6a12", "5f53dacd", "67d16ba7", "6a789bcc", "6d326601",
	"6d8ad336", "6eeb7890", "77e08e4d", "7851d1e3", "7b194d4e",
	"7b7c7649", "7b7d6c8c", "7d62af52", "80dab0c4", "844bce50",
	"845d4eb6", "8487cae1", "89570e0c", "8ad49303", "8b8b772e",
	"8bf50bd7", "8d53e68e", "8ec0e09c", "95905814", "960c0051",
	"96e311f2", "9ada242c", "9b4b98e1", "9c7df582", "9f0af794",
	"9f1b6470", "a1cc1c1c", "a50b8572", "ab920a80", "aeb035ff",
	"af25c691", "b01a92b8", "b730b691", "b912c5c0", "ba452792",
	"bacacb8b", "c22a6d79", "c3e0fae0", "c444cf96", "c4c5b223",
	"c5a369c4", "c8b3788a", "d55c762c", "d637f058", "d98183b6",
	"db18e025", "dbffb814", "e29141a9", "e30f01c5", "e50e3c44",
	"e56c6cb9", "e81fadf9", "e89d0d00", "ea7de3ff", "f2368bfd",
	"f353c80d", "f379e2b7", "f467fc1e", "f5ebeb33", "f7a8661b",
	"fa888894", "fa9180a6", "fd02f859", "fea67f7a",
}

var eightCharacterHex = regexp.MustCompile(`(?i)(^|[^0-9a-f])([0-9a-f]{8})([^0-9a-f]|$)`)

func TestDocLegacyReferencePrefixesAreAbsent(t *testing.T) {
	if len(migratedDocumentationPrefixes) != 104 {
		t.Fatalf("fixture has %d prefixes, want 104", len(migratedDocumentationPrefixes))
	}

	legacy := make(map[string]struct{}, len(migratedDocumentationPrefixes))
	for _, prefix := range migratedDocumentationPrefixes {
		if _, duplicate := legacy[prefix]; duplicate {
			t.Fatalf("fixture contains duplicate prefix %q", prefix)
		}
		legacy[prefix] = struct{}{}
	}

	repoRoot := repositoryRootForDocumentationTest(t)
	beforeObjects := gitObjectCountForDocumentationTest(t, repoRoot)

	for _, prefix := range migratedDocumentationPrefixes {
		cmd := exec.Command("git", "rev-parse", "--verify", prefix+"^{object}")
		cmd.Dir = repoRoot
		if output, err := cmd.CombinedOutput(); err == nil {
			t.Errorf("migrated prefix %q resolves to a git object: %s", prefix, strings.TrimSpace(string(output)))
		}
	}

	mappingPath := filepath.Join(repoRoot, "doc", "specs", "2026-08-27-uuid-to-integer-mapping.md")
	err := filepath.WalkDir(filepath.Join(repoRoot, "doc"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".md" || path == mappingPath {
			return nil
		}

		contents, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		for lineNumber, line := range strings.Split(string(contents), "\n") {
			matches := eightCharacterHex.FindAllStringSubmatch(line, -1)
			for _, match := range matches {
				prefix := strings.ToLower(match[2])
				if _, ok := legacy[prefix]; ok {
					t.Errorf("%s:%d: migrated legacy prefix %q remains", path, lineNumber+1, prefix)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	afterObjects := gitObjectCountForDocumentationTest(t, repoRoot)
	if afterObjects != beforeObjects {
		t.Fatalf("git object count changed while checking documentation: before=%d after=%d", beforeObjects, afterObjects)
	}
}

func repositoryRootForDocumentationTest(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("find documentation test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func gitObjectCountForDocumentationTest(t *testing.T, repoRoot string) int {
	t.Helper()
	cmd := exec.Command("git", "count-objects", "-v")
	cmd.Dir = repoRoot
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git count-objects: %v", err)
	}

	counts := map[string]int{}
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ": ")
		if !ok {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil {
			t.Fatalf("parse git count-objects field %q: %v", scanner.Text(), err)
		}
		counts[key] = parsed
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read git count-objects output: %v", err)
	}
	return counts["count"] + counts["in-pack"]
}
