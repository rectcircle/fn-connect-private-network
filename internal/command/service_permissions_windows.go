//go:build windows

package command

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsSystemSID         = "S-1-5-18"
	windowsAdministratorsSID = "S-1-5-32-544"
)

// preparePrivilegedCleanupDirectories applies the same trust checks used by
// the running service before MSI's SYSTEM cleanup helper can recover or remove
// resources from the persistent network journal.
func preparePrivilegedCleanupDirectories(role, stateDirectory, logPath string) error {
	if role != "client" {
		return nil
	}
	return preparePrivilegedDataDirectories(stateDirectory, logPath)
}

// preparePrivilegedDataDirectories protects service-owned state and logs from
// modification by standard users, even if ProgramData has inherited ACLs that
// are broader than expected. New directories receive their security descriptor
// atomically; existing trees are rejected if any entry has an untrusted owner.
func preparePrivilegedDataDirectories(stateDirectory, logPath string) error {
	paths := make(map[string]struct{}, 2)
	stateDirectory, err := filepath.Abs(stateDirectory)
	if err != nil {
		return fmt.Errorf("resolve state directory: %w", err)
	}
	paths[filepath.Clean(stateDirectory)] = struct{}{}
	logDirectory, err := filepath.Abs(filepath.Dir(logPath))
	if err != nil {
		return fmt.Errorf("resolve log directory: %w", err)
	}
	paths[filepath.Clean(logDirectory)] = struct{}{}

	orderedPaths := make([]string, 0, len(paths))
	for path := range paths {
		orderedPaths = append(orderedPaths, path)
	}
	sort.Strings(orderedPaths)
	for _, path := range orderedPaths {
		if err := securePrivilegedDirectory(path); err != nil {
			return fmt.Errorf("secure %s: %w", path, err)
		}
	}
	return nil
}

func securePrivilegedDirectory(path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	path = filepath.Clean(path)
	if err := rejectReparsePathComponents(path); err != nil {
		return err
	}
	if err := ensureSecureDirectoryPath(path); err != nil {
		return err
	}
	for _, directory := range protectedDirectoryComponents(path) {
		if err := verifyTrustedOwner(directory); err != nil {
			return err
		}
		if err := applyServiceACL(directory, true); err != nil {
			return fmt.Errorf("secure service directory component %s: %w", directory, err)
		}
	}
	return securePrivilegedTree(path)
}

func protectedDirectoryComponents(path string) []string {
	programData := os.Getenv("ProgramData")
	if programData == "" {
		programData = `C:\ProgramData`
	}
	programData, _ = filepath.Abs(programData)
	relative, err := filepath.Rel(programData, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return []string{path}
	}
	components := strings.Split(relative, string(filepath.Separator))
	current := programData
	protected := make([]string, 0, len(components))
	for _, component := range components {
		current = filepath.Join(current, component)
		protected = append(protected, current)
	}
	return protected
}

// ensureSecureDirectoryPath creates missing components with a protected
// descriptor rather than briefly exposing a default inherited DACL. Existing
// components are accepted only when their owner is SYSTEM or Administrators.
func ensureSecureDirectoryPath(path string) error {
	// Check every existing component before os.Stat or owner inspection. Those
	// APIs follow junctions/symlinks, which could otherwise redirect the
	// security checks to a different SYSTEM-owned directory.
	if err := rejectReparsePathComponents(path); err != nil {
		return err
	}
	volume := filepath.VolumeName(path)
	if filepath.Clean(path) == filepath.Clean(volume+string(filepath.Separator)) {
		return nil
	}
	if _, err := os.Stat(path); err == nil {
		return verifyTrustedOwner(path)
	} else if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) && !errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
		return err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return fmt.Errorf("cannot find existing parent directory for %s", path)
	}
	if err := ensureSecureDirectoryPath(parent); err != nil {
		return err
	}
	path16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("O:SYG:SYD:P(A;OICI;GA;;;SY)(A;OICI;GA;;;BA)")
	if err != nil {
		return fmt.Errorf("build initial service directory security descriptor: %w", err)
	}
	attributes := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}
	if err := windows.CreateDirectory(path16, attributes); err != nil {
		if !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return fmt.Errorf("create protected directory: %w", err)
		}
		// Another creator won the race. Never trust its directory until its
		// owner and reparse status have been checked below.
	}
	if err := rejectReparsePathComponents(path); err != nil {
		return err
	}
	return verifyTrustedOwner(path)
}

func securePrivilegedTree(path string) error {
	path16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(path16)
	if err != nil {
		return err
	}
	if attributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return fmt.Errorf("path is not a directory")
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("refusing to set service ACL on a reparse point")
	}
	if err := verifyTrustedOwner(path); err != nil {
		return err
	}
	if err := applyServiceACL(path, true); err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		child := filepath.Join(path, entry.Name())
		child16, err := windows.UTF16PtrFromString(child)
		if err != nil {
			return err
		}
		attributes, err := windows.GetFileAttributes(child16)
		if err != nil {
			return err
		}
		if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("refusing reparse point in service data tree: %s", child)
		}
		if err := verifyTrustedOwner(child); err != nil {
			return err
		}
		isDirectory := attributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
		if err := applyServiceACL(child, isDirectory); err != nil {
			return fmt.Errorf("apply service ACL to %s: %w", child, err)
		}
		if isDirectory {
			if err := securePrivilegedTree(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func verifyTrustedOwner(path string) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read owner for %s: %w", path, err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("read owner SID for %s: %w", path, err)
	}
	for _, sidText := range []string{windowsSystemSID, windowsAdministratorsSID} {
		trustedSID, err := windows.StringToSid(sidText)
		if err != nil {
			return err
		}
		if owner.Equals(trustedSID) {
			return nil
		}
	}
	return fmt.Errorf("refusing service data path with untrusted owner %s: %s; an administrator must inspect and secure this directory", owner.String(), path)
}

func applyServiceACL(path string, directory bool) error {
	acl, pins, err := serviceDirectoryACL()
	if err != nil {
		return err
	}
	defer pins.Unpin()
	securityInformation := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if directory {
		return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, securityInformation, nil, nil, acl, nil)
	}
	// Inheritance flags on the ACEs are harmless for files, but removing them
	// keeps the effective file ACL explicit and prevents accidental propagation.
	fileACL, err := serviceFileACL()
	if err != nil {
		return err
	}
	defer fileACL.pins.Unpin()
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, securityInformation, nil, nil, fileACL.acl, nil)
}

type pinnedACL struct {
	acl  *windows.ACL
	pins *runtime.Pinner
}

func serviceFileACL() (*pinnedACL, error) {
	entries := make([]windows.EXPLICIT_ACCESS, 0, 2)
	pinner := &runtime.Pinner{}
	for _, sidText := range []string{windowsSystemSID, windowsAdministratorsSID} {
		sid, err := windows.StringToSid(sidText)
		if err != nil {
			pinner.Unpin()
			return nil, err
		}
		pinner.Pin(sid)
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.SET_ACCESS,
			Trustee:           windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_WELL_KNOWN_GROUP, TrusteeValue: windows.TrusteeValueFromSID(sid)},
		})
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		pinner.Unpin()
		return nil, err
	}
	return &pinnedACL{acl: acl, pins: pinner}, nil
}

func rejectReparsePathComponents(path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(path)
	remaining := strings.TrimLeft(strings.TrimPrefix(path, volume), `\/`)
	current := volume + string(filepath.Separator)
	for _, component := range strings.FieldsFunc(remaining, func(r rune) bool {
		return r == '\\' || r == '/'
	}) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		path16, err := windows.UTF16PtrFromString(current)
		if err != nil {
			return err
		}
		attributes, err := windows.GetFileAttributes(path16)
		if err != nil {
			if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
				return nil
			}
			return err
		}
		if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("refusing reparse point in service data path: %s", current)
		}
	}
	return nil
}

func serviceDirectoryACL() (*windows.ACL, *runtime.Pinner, error) {
	entries := make([]windows.EXPLICIT_ACCESS, 0, 2)
	pinner := &runtime.Pinner{}
	for _, sidText := range []string{windowsSystemSID, windowsAdministratorsSID} {
		sid, err := windows.StringToSid(sidText)
		if err != nil {
			pinner.Unpin()
			return nil, nil, fmt.Errorf("parse service ACL SID %s: %w", sidText, err)
		}
		pinner.Pin(sid)
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.SET_ACCESS,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		pinner.Unpin()
		return nil, nil, fmt.Errorf("build service directory ACL: %w", err)
	}
	return acl, pinner, nil
}
