package backend

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// SemVersion represents a semantic version (major.minor.patch-prerelease).
type SemVersion struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease string
}

// ParseSemVersion parses a semantic version string into a SemVersion.
func ParseSemVersion(s string) (SemVersion, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")

	if s == "" {
		return SemVersion{}, fmt.Errorf("empty version string")
	}

	var prerelease string
	if idx := strings.Index(s, "-"); idx != -1 {
		prerelease = s[idx+1:]
		s = s[:idx]
	}

	parts := strings.Split(s, ".")
	var major, minor, patch int
	var err error

	if len(parts) >= 1 {
		major, err = strconv.Atoi(parts[0])
		if err != nil {
			return SemVersion{}, fmt.Errorf("invalid major version in %q: %w", s, err)
		}
	}
	if len(parts) >= 2 {
		minor, err = strconv.Atoi(parts[1])
		if err != nil {
			return SemVersion{}, fmt.Errorf("invalid minor version in %q: %w", s, err)
		}
	}
	if len(parts) >= 3 {
		patch, err = strconv.Atoi(parts[2])
		if err != nil {
			return SemVersion{}, fmt.Errorf("invalid patch version in %q: %w", s, err)
		}
	}

	return SemVersion{
		Major:      major,
		Minor:      minor,
		Patch:      patch,
		Prerelease: prerelease,
	}, nil
}

// Compare compares v to other:
// returns -1 if v < other, 0 if v == other, 1 if v > other.
func (v SemVersion) Compare(other SemVersion) int {
	if v.Major != other.Major {
		if v.Major < other.Major {
			return -1
		}
		return 1
	}
	if v.Minor != other.Minor {
		if v.Minor < other.Minor {
			return -1
		}
		return 1
	}
	if v.Patch != other.Patch {
		if v.Patch < other.Patch {
			return -1
		}
		return 1
	}
	if v.Prerelease == "" && other.Prerelease != "" {
		return 1
	}
	if v.Prerelease != "" && other.Prerelease == "" {
		return -1
	}
	if v.Prerelease < other.Prerelease {
		return -1
	}
	if v.Prerelease > other.Prerelease {
		return 1
	}
	return 0
}

func (v SemVersion) String() string {
	base := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Prerelease != "" {
		base += "-" + v.Prerelease
	}
	return base
}

type constraintOp string

const (
	opEqual        constraintOp = "="
	opNotEqual     constraintOp = "!="
	opGreaterThan  constraintOp = ">"
	opGreaterEqual constraintOp = ">="
	opLessThan     constraintOp = "<"
	opLessEqual    constraintOp = "<="
)

type versionConstraint struct {
	op      constraintOp
	version SemVersion
}

func (c versionConstraint) Matches(v SemVersion) bool {
	cmp := v.Compare(c.version)
	switch c.op {
	case opEqual:
		return cmp == 0
	case opNotEqual:
		return cmp != 0
	case opGreaterThan:
		return cmp > 0
	case opGreaterEqual:
		return cmp >= 0
	case opLessThan:
		return cmp < 0
	case opLessEqual:
		return cmp <= 0
	default:
		return false
	}
}

// VersionRange evaluates whether a detected version satisfies a version range requirement.
type VersionRange struct {
	raw         string
	constraints []versionConstraint
	allowAny    bool
}

// ParseVersionRange parses a version range expression such as ">= 7.68.0", ">= 2.0.0, < 3.0.0", "1.x", or "*".
func ParseVersionRange(rangeStr string) (VersionRange, error) {
	trimmed := strings.TrimSpace(rangeStr)
	if trimmed == "" || trimmed == "*" {
		return VersionRange{raw: rangeStr, allowAny: true}, nil
	}

	parts := strings.Split(trimmed, ",")
	var constraints []versionConstraint

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		var op constraintOp
		var verStr string

		if strings.HasPrefix(part, ">=") {
			op = opGreaterEqual
			verStr = strings.TrimSpace(part[2:])
		} else if strings.HasPrefix(part, "<=") {
			op = opLessEqual
			verStr = strings.TrimSpace(part[2:])
		} else if strings.HasPrefix(part, "!=") {
			op = opNotEqual
			verStr = strings.TrimSpace(part[2:])
		} else if strings.HasPrefix(part, "==") {
			op = opEqual
			verStr = strings.TrimSpace(part[2:])
		} else if strings.HasPrefix(part, "=") {
			op = opEqual
			verStr = strings.TrimSpace(part[1:])
		} else if strings.HasPrefix(part, ">") {
			op = opGreaterThan
			verStr = strings.TrimSpace(part[1:])
		} else if strings.HasPrefix(part, "<") {
			op = opLessThan
			verStr = strings.TrimSpace(part[1:])
		} else if strings.HasPrefix(part, "^") {
			baseVer, err := ParseSemVersion(strings.TrimSpace(part[1:]))
			if err != nil {
				return VersionRange{}, fmt.Errorf("invalid version in constraint %q: %w", part, err)
			}
			constraints = append(constraints, versionConstraint{op: opGreaterEqual, version: baseVer})
			nextMajor := SemVersion{Major: baseVer.Major + 1}
			constraints = append(constraints, versionConstraint{op: opLessThan, version: nextMajor})
			continue
		} else if strings.HasPrefix(part, "~") {
			baseVer, err := ParseSemVersion(strings.TrimSpace(part[1:]))
			if err != nil {
				return VersionRange{}, fmt.Errorf("invalid version in constraint %q: %w", part, err)
			}
			constraints = append(constraints, versionConstraint{op: opGreaterEqual, version: baseVer})
			nextMinor := SemVersion{Major: baseVer.Major, Minor: baseVer.Minor + 1}
			constraints = append(constraints, versionConstraint{op: opLessThan, version: nextMinor})
			continue
		} else if strings.HasSuffix(part, ".x") || strings.HasSuffix(part, ".*") {
			prefix := part[:len(part)-2]
			baseVer, err := ParseSemVersion(prefix)
			if err != nil {
				return VersionRange{}, fmt.Errorf("invalid wildcard version %q: %w", part, err)
			}
			constraints = append(constraints, versionConstraint{op: opGreaterEqual, version: baseVer})
			constraints = append(constraints, versionConstraint{op: opLessThan, version: SemVersion{Major: baseVer.Major + 1}})
			continue
		} else {
			op = opEqual
			verStr = part
		}

		ver, err := ParseSemVersion(verStr)
		if err != nil {
			return VersionRange{}, fmt.Errorf("invalid version %q in constraint %q: %w", verStr, part, err)
		}
		constraints = append(constraints, versionConstraint{op: op, version: ver})
	}

	return VersionRange{
		raw:         rangeStr,
		constraints: constraints,
	}, nil
}

// Satisfies reports whether the given version satisfies all constraints in the range.
func (vr VersionRange) Satisfies(v SemVersion) bool {
	if vr.allowAny {
		return true
	}
	for _, c := range vr.constraints {
		if !c.Matches(v) {
			return false
		}
	}
	return true
}

func (vr VersionRange) String() string {
	return vr.raw
}

var versionPattern = regexp.MustCompile(`(?i)(?:version\s+)?v?(\d+\.\d+(?:\.\d+)?(?:-[0-9A-Za-z.-]+)?)`)

// ExtractVersion finds and extracts the first semantic version pattern from command output text.
func ExtractVersion(output string) (string, error) {
	matches := versionPattern.FindStringSubmatch(output)
	if len(matches) < 2 {
		return "", fmt.Errorf("no version string found in output: %q", output)
	}
	return matches[1], nil
}

// WrongVersionError clearly names the tool and the detected version when it falls outside declared range.
type WrongVersionError struct {
	Tool            string
	DetectedVersion string
	DeclaredRange   string
}

func (e *WrongVersionError) Error() string {
	return fmt.Sprintf("backend %q: detected version %q is outside declared range %q", e.Tool, e.DetectedVersion, e.DeclaredRange)
}

// ValidateVersion checks whether the detected version string satisfies the declared range for the named tool.
// If outside the declared range, it returns a WrongVersionError naming both the tool and the detected version.
func ValidateVersion(tool string, detectedVersion string, declaredRange string) error {
	vr, err := ParseVersionRange(declaredRange)
	if err != nil {
		return fmt.Errorf("invalid version range %q for %q: %w", declaredRange, tool, err)
	}
	v, err := ParseSemVersion(detectedVersion)
	if err != nil {
		return &WrongVersionError{
			Tool:            tool,
			DetectedVersion: detectedVersion,
			DeclaredRange:   declaredRange,
		}
	}
	if !vr.Satisfies(v) {
		return &WrongVersionError{
			Tool:            tool,
			DetectedVersion: detectedVersion,
			DeclaredRange:   declaredRange,
		}
	}
	return nil
}
