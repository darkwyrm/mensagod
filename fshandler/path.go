package fshandler

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/viper"
)

// ErrBadPath is returned when a bad path is passed to a function
var ErrBadPath = errors.New("invalid path")

// AnPath encapsulates all the translation between a standard Mensago path into whatever format
// a filesystem needs. These are leveraged by the filesytem providers to assist with going between
// the two realms
type AnPath interface {
	// PathType returns a string which indicates the kind of implementation the path object
	// handles. It is expected to be all lowercase. As with FSProvider, subtypes can be indicated
	// by a period separator.
	PathType() string

	// FromPath simply assigns to the object from the Mensago path of another
	FromPath(path AnPath) error

	// Set expects an Mensago path. If the path is invalid or some other error occurs, the
	// object is not changed and the error is returned.
	Set(path string) error

	// ProviderPath returns a string which
	ProviderPath() string
	MensagoPath() string
}

// LocalAnPath is an AnPath interface that interacts with the local filesystem. It handles the
// operating system-specific path separators, among other things.
type LocalAnPath struct {
	// Path contains the path as formatted for the Mensago platform
	Path string

	// LocalPath holds the path as needed by the local filesystem
	LocalPath string
}

// PathType returns the type of path handled
func (ap *LocalAnPath) PathType() string {
	return "local"
}

// FromPath assigns an Mensago path to the object
func (ap *LocalAnPath) FromPath(path AnPath) error {
	return ap.Set(path.MensagoPath())
}

// Set assigns an Mensago path to the object
func (ap *LocalAnPath) Set(path string) error {

	if path == "" {
		ap.LocalPath = ""
		ap.Path = ""
		return nil
	}

	if !ValidateMensagoPath(path) {
		return ErrBadPath
	}

	ap.Path = path

	workspaceRoot := viper.GetString("global.workspace_dir")
	pathParts := strings.Split(path, " ")
	ap.LocalPath = filepath.Join(workspaceRoot,
		strings.Join(pathParts[1:], string(filepath.Separator)))

	return nil
}

// ProviderPath returns the local filesystem version of the path set
func (ap *LocalAnPath) ProviderPath() string {
	return ap.LocalPath
}

// MensagoPath returns the Mensago path version of the path set
func (ap *LocalAnPath) MensagoPath() string {
	return ap.Path
}

// ValidateMensagoPath confirms the validity of an Mensago path
func ValidateMensagoPath(path string) bool {

	// Just a slash is also valid -- refers to the workspace root directory
	if path == "/" {
		return true
	}

	pattern := regexp.MustCompile(
		"^/( [0-9a-fA-F]{8}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{12})*" +
			"( [0-9]+\\.[0-9]+\\." +
			"[0-9a-fA-F]{8}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{12})*$")

	return pattern.MatchString(path)
}

// ValidateFileName returns whether or not a filename conforms to the format expected by the
// platform
func ValidateFileName(filename string) bool {
	pattern := regexp.MustCompile(
		"^[0-9]+\\.[0-9]+\\." +
			"[0-9a-fA-F]{8}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{12}$")
	return pattern.MatchString(filename)
}

// ValidateTempFileName returns whether or not a filename for a temp file conforms to the format
// expected by the platform
func ValidateTempFileName(filename string) bool {
	pattern := regexp.MustCompile(
		"^[0-9]+\\." +
			"[0-9a-fA-F]{8}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{12}$")
	return pattern.MatchString(filename)
}

// GenerateFileName creates a filename matching the format expected by the Mensago platform
func GenerateFileName(filesize int) string {
	return fmt.Sprintf("%d.%d.%s", time.Now().Unix(), filesize, uuid.New().String())
}

// GenerateTempFileName creates a temporary file name. It is similar to an Mensago file name
// except that the file's size is not included.
func GenerateTempFileName() string {
	return fmt.Sprintf("%d.%s", time.Now().Unix(), uuid.New().String())
}
