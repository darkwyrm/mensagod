package dbhandler

// This module is for abstracting away all the messy details of interacting with the database.
// By doing so, it will be easier to add support for databases other than Postgresql. It will also
// eliminate cluttering up the otherwise-clean Go code with the ugly SQL queries.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"database/sql"

	"github.com/darkwyrm/anselusd/cryptostring"
	"github.com/darkwyrm/anselusd/ezcrypt"
	"github.com/darkwyrm/anselusd/keycard"
	"github.com/darkwyrm/anselusd/logging"
	"github.com/darkwyrm/gostringlist"
	"github.com/everlastingbeta/diceware"
	"github.com/lib/pq"
	"github.com/spf13/viper"
	"golang.org/x/crypto/argon2"
)

var (
	connected bool
	serverLog *log.Logger
	dbConn    *sql.DB
)

// This internal function is for turning a string into an Argon2 password hash.
func hashPassword(password string) string {
	mode := viper.GetString("security.password_security")

	var argonRAM, argonIterations, argonSaltLength, argonKeyLength uint32
	var argonThreads uint8

	if strings.ToLower(mode) == "enhanced" {
		// LUDICROUS SPEED! GO!
		argonRAM = 1073741824 // 1GB of RAM
		argonIterations = 10
		argonThreads = 8
		argonSaltLength = 24
		argonKeyLength = 48
	} else {
		argonRAM = 65536 // 64MB of RAM
		argonIterations = 3
		argonThreads = 4
		argonSaltLength = 16
		argonKeyLength = 32
	}

	salt := make([]byte, argonSaltLength)
	_, err := rand.Read(salt)
	if err != nil {
		logging.Writef("Failure reading random bytes: %s", err.Error())
		return ""
	}

	passhash := argon2.IDKey([]byte(password), salt, argonIterations, argonRAM, argonThreads,
		argonKeyLength)

	// Although base85 encoding is used wherever possible, base64 is used here because of a
	// potential collision: base85 uses the $ character and argon2 hash strings use it as a
	// field delimiter. Not a huge deal as it just uses a little extra disk storage and doesn't
	// get transmitted over the network
	passString := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonRAM, argonIterations, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(passhash))
	return passString
}

// This internal function takes a password and the Argon2 hash to verify against, gets the
// parameters from the hash, applies them to the supplied password, and returns whether or not
// they match and an error value
func verifyHash(password string, hashPass string) (bool, error) {
	splitValues := strings.Split(hashPass, "$")
	if len(splitValues) != 6 {
		return false, errors.New("Invalid Argon hash string")
	}

	var version int
	_, err := fmt.Sscanf(splitValues[2], "v=%d", &version)
	if err != nil {
		return false, err
	}
	if version != argon2.Version {
		return false, errors.New("Unsupported Argon version")
	}

	var ramUsage, iterations uint32
	var parallelism uint8
	_, err = fmt.Sscanf(splitValues[3], "m=%d,t=%d,p=%d", &ramUsage, &iterations, &parallelism)
	if err != nil {
		return false, err
	}

	var salt []byte
	salt, err = base64.RawStdEncoding.DecodeString(splitValues[4])
	if err != nil {
		return false, err
	}

	var savedHash []byte
	savedHash, err = base64.RawStdEncoding.DecodeString(splitValues[5])
	if err != nil {
		return false, err
	}

	passhash := argon2.IDKey([]byte(password), salt, iterations, ramUsage, parallelism,
		uint32(len(savedHash)))

	return (subtle.ConstantTimeCompare(passhash, savedHash) == 1), nil
}

// Connect utilizes the viper config system and connects to the specified database. Because
// problems in the connection are almost always fatal to the successful continuation of the server
// daemon, if there are problems, it logs the problem and exits the main process.
func Connect() {
	if viper.GetString("database.engine") != "postgresql" {
		logging.Write("Database password not set in config file. Exiting.")
		logging.Shutdown()
		os.Exit(1)
	}

	connString := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		viper.GetString("database.ip"), viper.GetString("database.port"),
		viper.GetString("database.user"), viper.GetString("database.password"),
		viper.GetString("database.name"))

	var err error
	dbConn, err = sql.Open("postgres", connString)
	if err != nil {
		logging.Writef("Failed to open database connection. Exiting. Error: %s", err.Error())
		logging.Shutdown()
		os.Exit(1)
	}
	// Calling Ping() is required because Open() just validates the settings passed
	err = dbConn.Ping()
	if err != nil {
		logging.Writef("Failed to open database connection. Exiting. Error: %s", err.Error())
		logging.Shutdown()
		os.Exit(1)
	}
	connected = true
}

// Disconnect shuts down the connection to the database server
func Disconnect() {
	if IsConnected() {
		dbConn.Close()
	}
}

// IsConnected returns a boolean if it has successfully connected to the Anselus server database
func IsConnected() bool {
	return connected
}

// LogFailure adds an entry to the database of a failure which needs tracked. This
// includes a type (workspace, password, recipient), the source (IP address, WID),
// and the timestamp of the failure.
// This function will check the server configuration and if the failure has
// exceeded the threshold for that type of failure, then a lockout timestamp will
// be set.
func LogFailure(failType string, id string, source string) error {

	// failure type can only be one of three possible values
	switch failType {
	case "workspace", "password", "recipient":
		// do nothing -- everything's OK
	default:
		return errors.New("LogFailure: failure type must be 'workspace', 'password', or 'recipient',")
	}

	// source may only be an IP address or a UUID
	if source == "" {
		return errors.New("LogFailure: source may not be empty")
	} else if net.ParseIP(source) == nil && !ValidateUUID(source) {
		return errors.New(strings.Join([]string{"LogFailure: bad source ", source}, ""))
	}

	// Timestamp must be ISO8601 without a timezone ('Z' suffix allowable)
	timeString := time.Now().UTC().Format(time.RFC3339)

	// Now that the error-checking is out of the way, we can actually update the db. :)
	row := dbConn.QueryRow(`SELECT count FROM failure_log WHERE type=$1 AND source=$2`,
		failType, source)
	var failCount int
	err := row.Scan(&failCount)
	if err != nil {
		// Existing fail count. Increment value, check for lockout, and update table
		failCount++
		if failCount >= viper.GetInt("security.max_failures") {
			// Failure threshold exceeded. Calculate lockout timestamp and update db
			lockout := time.Now().UTC()
			delay, _ := time.ParseDuration(fmt.Sprintf("%dm",
				viper.GetInt64("security.lockout_delay_min")))
			lockout.Add(delay)
			sqlStatement := `
				UPDATE failure_log 
				SET count=$1, last_failure=$2, lockout_until=$3
				WHERE type=$4 AND source=$5 AND id=$6`
			_, err = dbConn.Exec(sqlStatement, failCount, timeString, lockout.Format(time.RFC3339),
				failType, source, id)
			if err != nil {
				logging.Write("dbhandler.LogFailure: failed to update failure log")
				return err
			}
		} else {
			// Within threshold, so just update values
			sqlStatement := `
				UPDATE failure_log 
				SET count=$1, last_failure=$2 
				WHERE type=$3 AND source=$4 and wid=$5`
			_, err = dbConn.Exec(sqlStatement, failCount, timeString, failType, source, id)
			if err != nil {
				logging.Write("dbhandler.LogFailure: failed to update failure log")
				return err
			}
		}
	} else {
		sqlStatement := `INSERT INTO failure_log(type, source, wid, count, last_failure)
			VALUES($1, $2, $3, $4, $5)`
		_, err = dbConn.Exec(sqlStatement, failType, source, id, failCount, timeString)
		if err != nil {
			logging.Write("dbhandler.LogFailure: failed to update failure log")
			return err
		}
	}

	return nil
}

// ValidateUUID just returns whether or not a string is a valid UUID.
func ValidateUUID(uuid string) bool {
	pattern := regexp.MustCompile("[\\da-fA-F]{8}-?[\\da-fA-F]{4}-?[\\da-fA-F]{4}-?[\\da-fA-F]{4}-?[\\da-fA-F]{12}")
	if len(uuid) != 36 && len(uuid) != 32 {
		return false
	}
	return pattern.MatchString(uuid)
}

// GetAnselusAddressType returns the type of address given to it. It returns 0 when there is an
// error, 1 when given a valid workspace address, and 2 when given a valid Anselus address
func GetAnselusAddressType(addr string) int {

	parts := strings.Split(addr, "/")
	if len(parts) != 2 {
		return 0
	}

	// Validate the domain portion of the address
	pattern := regexp.MustCompile("([a-zA-Z0-9]+\x2E)+[a-zA-Z0-9]+")
	if !pattern.MatchString(parts[1]) {
		return 0
	}

	// Is this a workspace address?
	if ValidateUUID(parts[0]) {
		return 1
	}

	pattern = regexp.MustCompile("[\\\"]|[[:space:]]")
	if pattern.MatchString(parts[0]) {
		return 0
	}
	return 2
}

// ResolveAddress returns the WID corresponding to an Anselus address.
func ResolveAddress(addr string) (string, error) {
	parts := strings.Split(addr, "/")
	if len(parts) != 2 {
		return "", errors.New("invalid address")
	}

	// Validate the domain portion of the address
	pattern := regexp.MustCompile("([a-zA-Z0-9]+\x2E)+[a-zA-Z0-9]+")
	if !pattern.MatchString(parts[1]) {
		return "", errors.New("invalid domain")
	}

	// Is this a workspace address?
	isWid := ValidateUUID(parts[0])

	pattern = regexp.MustCompile("[\\\"]|[[:space:]]")
	if pattern.MatchString(parts[0]) {
		return "", errors.New("invalid user id")
	}

	if isWid {
		// If the address is a workspace address, then all we have to do is confirm that the
		// workspace exists -- workspace IDs are unique across an organization, not just a domain
		row := dbConn.QueryRow(`SELECT wtype FROM workspaces WHERE wid=$1`, parts[0])
		var wtype string
		err := row.Scan(&wtype)

		if err != nil {
			if err == sql.ErrNoRows {
				// No entry in the table
				return "", errors.New("workspace not found")
			}
			return "", err
		}

		if wtype == "alias" {
			row := dbConn.QueryRow(`SELECT wid FROM aliases WHERE wid=$1`, parts[0])
			var targetAddr string
			err := row.Scan(&targetAddr)
			if err != nil {
				return "", err
			}

			return targetAddr, nil
		}
		return addr, nil
	}

	row := dbConn.QueryRow(`SELECT wid,domain FROM workspaces WHERE uid=$1`, parts[0])
	var wid, domain string

	err := row.Scan(&wid, &domain)
	if err != nil {
		if err == sql.ErrNoRows {
			// No entry in the table
			return "", errors.New("workspace not found")
		}
		return "", err
	}

	return wid, nil
}

// CheckLockout corresponds to LogFailure() in that it checks to see if said
// source has a lockout timestamp and returns it if there is or an empty string if not.
// It also has the added benefit of resetting a counter to 0 if there is an expired
// lockout for a particular source. The ID parameter is a string specific to the failure type.
// For example, for logins, it is the workspace ID. For preregistration codes, it is the IP
// address of the remote host.
func CheckLockout(failType string, id string, source string) (string, error) {
	row := dbConn.QueryRow(`SELECT lockout_until FROM failure_log 
		WHERE id=$1 and source=$2`, id, source)

	var locktime string
	err := row.Scan(&locktime)

	if err != nil {
		if err == sql.ErrNoRows {
			// No entry in the table, so obviously no lockout
			return "", nil
		}
		logging.Write("dbhandler.CheckLockout: db error")
		return "", err
	}

	if len(locktime) < 1 {
		return locktime, nil
	}

	var lockstamp time.Time
	lockstamp, err = time.Parse(time.RFC3339, locktime)
	if err != nil {
		logging.Write("dbhandler.CheckLockout: bad timestamp in database")
		return "", err
	}

	// If there is an expired lockout for this address, delete it
	if lockstamp.Before(time.Now().UTC()) {
		sqlStatement := `DELETE FROM failure_log
		WHERE failtype=$1 AND source=$2 AND lockout_until=$3 `
		_, err = dbConn.Exec(sqlStatement, failType, source, locktime)
		if err != nil {
			logging.Write("dbhandler.CheckLockout: couldn't remove lockout from db")
			return "", err
		}
		return "", nil
	}

	return locktime, nil
}

// CheckPasscode checks the validity of a workspace/passcode combination
func CheckPasscode(wid string, passcode string) (bool, error) {
	return false, errors.New("unimplemented")
}

// DeletePasscode deletes a workspace/passcode combination
func DeletePasscode(wid string, passcode string) (bool, error) {
	return false, errors.New("unimplemented")
}

// RemoveExpiredPasscodes removes any workspace/passcode combination entries which are expired
func RemoveExpiredPasscodes() error {
	return errors.New("unimplemented")
}

// ResetPassword adds a reset code combination to the database for later authentication by the
// user. All parameters are expected to be populated.
func ResetPassword(wid string, passcode string, expires string) error {
	return errors.New("unimplemented")
}

// SetPassword does just that: sets the password for a workspace. It returns a boolean state,
// indicating a match (or lack thereof) and an error state. It will take any input string of up to
// 64 characters and store it in the database.
func SetPassword(wid string, password string) error {
	if len(password) > 128 {
		return errors.New("Password string has a maximum 128 characters")
	}
	passHash := hashPassword(password)
	_, err := dbConn.Exec(`UPDATE workspaces SET password=$1 WHERE wid=$2`, passHash, wid)
	return err
}

// CheckPassword checks a password hash against the one stored in the database. It returns true
// if the two hashes match. It does not perform any validity checking of the input--this should be
// done when the input is received from the user.
func CheckPassword(wid string, password string) (bool, error) {
	row := dbConn.QueryRow(`SELECT password FROM workspaces WHERE wid=$1`, wid)

	var dbhash string
	err := row.Scan(&dbhash)
	if err != nil {
		return false, err
	}

	return verifyHash(password, dbhash)
}

// SetWorkspaceStatus sets the status of a workspace. Valid values are "disabled", "active", and
// "approved". Although a workspace can also have a status of "awaiting", this state is internal
// to the dbhandler API and cannot be set directly.
func SetWorkspaceStatus(wid string, status string) error {
	realStatus := strings.ToLower(status)

	if realStatus == "awaiting" {
		return fmt.Errorf("awaiting is an internal-only workspace status")
	}
	if realStatus != "active" && realStatus != "disabled" && realStatus != "approved" {
		return fmt.Errorf("%s is not a valid status", realStatus)
	}
	if !ValidateUUID(wid) {
		return fmt.Errorf("%s is not a valid workspace ID", wid)
	}
	var err error
	_, err = dbConn.Exec(`UPDATE workspaces SET status=$1 WHERE wid=$2`, status, wid)
	return err
}

// AddDevice is used for adding a device to a workspace. It generates a new session string for the
// device, adds it to the device table, sets the device status, and returns the session string for
// the new device.
func AddDevice(wid string, devid string, devkey cryptostring.CryptoString, status string) error {
	var err error
	sqlStatement := `INSERT INTO iwkspc_devices(wid, devid, devkey, status) ` +
		`VALUES($1, $2, $3, $4)`
	_, err = dbConn.Exec(sqlStatement, wid, devid, devkey.AsString(), status)
	if err != nil {
		return err
	}
	return nil
}

// RemoveDevice removes a session string for a workspace. It returns true if successful and false
// if not.
func RemoveDevice(wid string, devid string) (bool, error) {
	if len(devid) != 40 {
		return false, errors.New("invalid session string")
	}
	_, err := dbConn.Exec(`DELETE FROM iwkspc_devices WHERE wid=$1 AND devid=$2`, wid, devid)
	if err != nil {
		return false, nil
	}
	return true, nil
}

// CheckDevice checks a session string on a workspace and returns true or false if there is a match.
func CheckDevice(wid string, devid string, devkey string) (bool, error) {
	row := dbConn.QueryRow(`SELECT status FROM iwkspc_devices WHERE wid=$1 AND 
		devid=$2 AND devkey=$3`, wid, devid, devkey)

	var widStatus string
	err := row.Scan(&widStatus)

	switch err {
	case sql.ErrNoRows:
		return false, nil
	case nil:
		return true, nil
	default:
		return false, err
	}
}

// UpdateDevice replaces a device's old key with a new one
func UpdateDevice(wid string, devid string, oldkey string, newkey string) error {
	_, err := dbConn.Exec(`UPDATE iwkspc_devices SET devkey=$1 WHERE wid=$2 AND 
		devid=$3 AND devkey=$4`, newkey, wid, devid, oldkey)

	return err
}

// AddWorkspace is used for adding a workspace to a server. Upon failure, it returns the error
// state for the failure. It makes the necessary database modifications and creates the folder for
// the workspace in the filesystem. Note that this function is strictly for adding workspaces for
// individuals. Shared workspaces are not yet supported/implemented. Status may be 'active',
// 'pending', or 'disabled'.
func AddWorkspace(wid string, uid string, domain string, password string, status string,
	wtype string) error {
	passString := hashPassword(password)

	// wid, uid, domain, wtype, status, password
	var err error
	_, err = dbConn.Exec(`INSERT INTO workspaces(wid, uid, domain, password, status, wtype) `+
		`VALUES($1, $2, $3, $4, $5, $6)`,
		wid, uid, domain, passString, status, wtype)
	return err
}

// RemoveWorkspace deletes a workspace. It returns an error if unsuccessful. Note that this does
// not remove all information about the workspace. WIDs and UIDs may not be reused for security
// purposes, so the uid and wid attached to the workspace will remain in the database for this
// reason
func RemoveWorkspace(wid string) error {
	var sqlCommands = []string{
		`UPDATE workspaces SET password='-',status='deleted' WHERE wid=$1`,
		`DELETE FROM iwkspc_folders WHERE wid=$1`,
	}
	for _, sqlCmd := range sqlCommands {
		_, err := dbConn.Exec(sqlCmd, wid)
		if err != nil {
			return err
		}
	}
	return nil
}

// CheckWorkspace checks to see if a workspace exists. If the workspace does exist,
// True is returned along with a string containing the workspace's status. If the
// workspace does not exist, it returns false and an empty string. The workspace
// status can be 'active', 'pending', or 'disabled'. Preregistered workspaces have the status
// 'approved'. Note that this function does not check the validity of the WID string passed to it.
// This should be done when the input is received from the user.
func CheckWorkspace(wid string) (bool, string) {
	row := dbConn.QueryRow(`SELECT status FROM workspaces WHERE wid=$1`, wid)

	var widStatus string
	err := row.Scan(&widStatus)

	switch err {
	case sql.ErrNoRows:
		break
	case nil:
		return true, widStatus
	case err.(*pq.Error):
		logging.Writef("dbhandler.CheckWorkspace: PostgreSQL error reading workspaces: %s",
			err.Error())
		return false, ""
	default:
		logging.Writef("dbhandler.CheckWorkspace: unexpected error reading workspaces: %s",
			err.Error())
		return false, ""
	}

	row = dbConn.QueryRow(`SELECT wid FROM prereg WHERE wid=$1`, wid)
	err = row.Scan(&widStatus)

	switch err {
	case sql.ErrNoRows:
		return false, ""
	case nil:
		return true, "approved"
	case err.(*pq.Error):
		logging.Writef("dbhandler.CheckWorkspace: PostgreSQL error reading prereg: %s",
			err.Error())
		return false, ""
	default:
		logging.Writef("dbhandler.CheckWorkspace: unexpected error reading prereg: %s",
			err.Error())
		return false, ""
	}
}

// CheckUserID works the same as CheckWorkspace except that it checks for user IDs
func CheckUserID(uid string) (bool, string) {
	row := dbConn.QueryRow(`SELECT status FROM workspaces WHERE uid=$1`, uid)

	var widStatus string
	err := row.Scan(&widStatus)

	switch err {
	case sql.ErrNoRows:
		break
	case nil:
		return true, widStatus
	case err.(*pq.Error):
		logging.Writef("dbhandler.CheckUserID: PostgreSQL error reading workspaces: %s",
			err.Error())
		return false, ""
	default:
		logging.Writef("dbhandler.CheckUserID: unexpected error reading workspaces: %s",
			err.Error())
		return false, ""
	}

	row = dbConn.QueryRow(`SELECT uid FROM prereg WHERE uid=$1`, uid)
	err = row.Scan(&widStatus)

	switch err {
	case sql.ErrNoRows:
		return false, ""
	case nil:
		return true, "approved"
	case err.(*pq.Error):
		logging.Writef("dbhandler.CheckUserID: PostgreSQL error reading prereg: %s",
			err.Error())
		return false, ""
	default:
		logging.Writef("dbhandler.CheckUserID: unexpected error reading prereg: %s",
			err.Error())
		return false, ""
	}
}

// PreregWorkspace preregisters a workspace, adding a specified wid to the database and returns
// a randomly-generated registration code needed to authenticate the first login. Registration
// codes are stored in the clear, but that's merely because if an attacker already has access to
// the server to see the codes, the attacker can easily create new workspaces.
func PreregWorkspace(wid string, uid string, domain string, wordList *diceware.Wordlist,
	wordcount int) (string, error) {

	if len(wid) > 36 || len(uid) > 128 {
		return "", errors.New("Bad parameter length")
	}

	if len(uid) > 0 {
		row := dbConn.QueryRow(`SELECT uid FROM prereg WHERE uid=$1`, uid)
		var hasuid string
		err := row.Scan(&hasuid)

		if hasuid != "" {
			return "", errors.New("uid exists")
		}

		switch err {
		case sql.ErrNoRows:
			break
		case err.(*pq.Error):
			logging.Writef("dbhandler.PreregWorkspace: PostgreSQL error reading prereg: %s",
				err.Error())
			return "", err
		default:
			logging.Writef("dbhandler.PreregWorkspace: unexpected error reading prereg: %s",
				err.Error())
			return "", err
		}
	}

	regcode, err := diceware.RollWords(wordcount, "-", *wordList)

	_, err = dbConn.Exec(`INSERT INTO prereg(wid, uid, domain, regcode) VALUES($1, $2, $3, $4)`,
		wid, uid, domain, regcode)

	return regcode, err
}

// CheckRegCode handles authenticating a host using a user/workspace ID and registration
// code provided by PreregWorkspace. Based on authentication it either returns the workspace ID
// (success) or an empty string (failure). An error is returned only if authentication was not
// successful. The caller is still responsible for performing the necessary steps to add the
// workspace to the database.
func CheckRegCode(id string, domain string, iswid bool, regcode string) (string, string, error) {
	var wid, uid string
	if iswid {
		row := dbConn.QueryRow(`SELECT wid,uid FROM prereg WHERE regcode = $1 AND domain = $2`,
			regcode, domain)
		err := row.Scan(&wid, &uid)
		if err != nil {
			if err == sql.ErrNoRows {
				// No entry in the table
				return "", "", errors.New("regcode not found")
			}
			return "", "", err
		}

		if wid == id {
			return wid, uid, nil
		}
		return "", "", errors.New("wid mismatch")
	}

	row := dbConn.QueryRow(`SELECT wid,uid FROM prereg WHERE regcode = $1 AND uid = $2 `+
		`AND domain = $3`, regcode, id, domain)
	err := row.Scan(&wid, &uid)
	if err != nil {
		if err == sql.ErrNoRows {
			// No entry in the table
			return "", "", errors.New("regcode not found")
		}
		return "", "", err
	}

	return wid, uid, nil
}

// DeleteRegCode removes preregistration data from the database.
func DeleteRegCode(id string, domain string, iswid bool, regcode string) error {

	var err error
	if iswid {
		_, err = dbConn.Exec(`DELETE FROM prereg WHERE wid = $1 AND regcode = $2 AND domain = $3`,
			id, regcode, domain)
	} else {
		_, err = dbConn.Exec(`DELETE FROM prereg WHERE uid = $1 AND regcode = $2 AND domain = $3`,
			id, regcode, domain)
	}

	return err
}

// GetOrgEntries pulls one or more entries from the database. If an end index is not desired, set
// it to 0. Passing a starting index of 0 will return the current entry for the organization.
func GetOrgEntries(startIndex int, endIndex int) ([]string, error) {
	out := make([]string, 0, 10)

	if startIndex < 1 {
		// If given a 0 or negative number, we return just the current entry.
		row := dbConn.QueryRow(`SELECT entry FROM keycards WHERE owner = 'organization' ` +
			`ORDER BY index DESC LIMIT 1`)

		var entry string
		err := row.Scan(&entry)
		if err == nil {
			out = append(out, entry)
		}
		return out, err

	} else if endIndex >= 1 {
		// Given both a start and end index
		if endIndex < startIndex {
			return out, nil
		}
		rows, err := dbConn.Query(`SELECT entry FROM keycards WHERE owner = 'organization' `+
			`AND index >= $1 AND index <= $2 ORDER BY index`, startIndex, endIndex)
		if err != nil {
			return out, err
		}
		defer rows.Close()

		for rows.Next() {
			var entry string
			err := rows.Scan(&entry)
			if err != nil {
				return out, err
			}
			out = append(out, entry)
		}

	} else {
		// Given just a start index
		rows, err := dbConn.Query(`SELECT entry FROM keycards WHERE owner = 'organization' `+
			`AND index >= $1 ORDER BY index`, startIndex)
		if err != nil {
			return out, err
		}
		defer rows.Close()

		for rows.Next() {
			var entry string
			err := rows.Scan(&entry)
			if err != nil {
				return out, err
			}
			out = append(out, entry)
		}

	}
	return out, nil
}

// GetUserEntries pulls one or more entries from the database. If an end index is not desired, set
// it to 0. Passing a starting index of 0 will return the current entry for the workspace specified.
func GetUserEntries(wid string, startIndex int, endIndex int) ([]string, error) {
	out := make([]string, 0, 10)

	if startIndex < 1 {
		// If given a 0 or negative number, we return just the current entry.
		row := dbConn.QueryRow(`SELECT entry FROM keycards WHERE owner = $1 `+
			`ORDER BY index DESC LIMIT 1`, wid)

		var entry string
		err := row.Scan(&entry)
		if err == nil {
			out = append(out, entry)
		}
		return out, err

	} else if endIndex >= 1 {
		// Given both a start and end index
		if endIndex < startIndex {
			return out, nil
		}
		rows, err := dbConn.Query(`SELECT entry FROM keycards WHERE owner = $1 `+
			`AND index >= $2 AND index <= $3 ORDER BY index`, wid, startIndex, endIndex)
		if err != nil {
			return out, err
		}
		defer rows.Close()

		for rows.Next() {
			var entry string
			err := rows.Scan(&entry)
			if err != nil {
				return out, err
			}
			out = append(out, entry)
		}

	} else {
		// Given just a start index
		rows, err := dbConn.Query(`SELECT entry FROM keycards WHERE owner = $1 `+
			`AND index >= $2 ORDER BY index`, wid, startIndex)
		if err != nil {
			return out, err
		}
		defer rows.Close()

		for rows.Next() {
			var entry string
			err := rows.Scan(&entry)
			if err != nil {
				return out, err
			}
			out = append(out, entry)
		}

	}
	return out, nil
}

// GetLastEntry returns the last entry in the database
func GetLastEntry() (string, error) {
	row := dbConn.QueryRow(`SELECT entry FROM keycards ORDER BY rowid DESC LIMIT 1`)

	var entry string
	err := row.Scan(&entry)
	return entry, err
}

// AddEntry adds an entry to the database. The caller is responsible for validation of *ALL* data
// passed to this command.
func AddEntry(entry *keycard.Entry) error {
	var owner string
	if entry.Fields["Type"] == "Organization" {
		owner = "organization"
	} else {
		owner = entry.Fields["Workspace-ID"]
	}

	var err error
	_, err = dbConn.Exec(`INSERT INTO keycards(owner, creationtime, index, entry, fingerprint) `+
		`VALUES($1, $2, $3, $4, $5)`, owner, entry.Fields["Timestamp"], entry.Fields["Index"],
		string(entry.MakeByteString(-1)), entry.Hash)
	return err
}

// GetPrimarySigningKey obtains the organization's primary signing key as an CryptoString
func GetPrimarySigningKey() (string, error) {
	row := dbConn.QueryRow(`SELECT privkey FROM orgkeys WHERE purpose = 'sign' ` +
		`ORDER BY rowid DESC LIMIT 1`)

	var psk string
	err := row.Scan(&psk)
	if err == nil {
		return psk, nil
	}
	return "", err
}

// GetEncryptionPair returns the organization's encryption keypair as an EncryptionPair
func GetEncryptionPair() (*ezcrypt.EncryptionPair, error) {
	row := dbConn.QueryRow(`SELECT pubkey,privkey FROM orgkeys WHERE purpose = 'encrypt' ` +
		`ORDER BY rowid DESC LIMIT 1`)

	var pubkey, privkey string
	err := row.Scan(&pubkey, &privkey)
	if err == nil {
		keypair := ezcrypt.NewEncryptionPair(cryptostring.New(pubkey),
			cryptostring.New(privkey))
		return keypair, nil
	}
	return nil, err
}

// GetAliases returns a StringList containing the aliases pointing to the specified WID
func GetAliases(wid string) (gostringlist.StringList, error) {
	var out gostringlist.StringList
	rows, err := dbConn.Query(`SELECT alias FROM alias WHERE wid=$1`, wid)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	for rows.Next() {
		var entry string
		err := rows.Scan(&entry)
		if err != nil {
			return out, err
		}
		out.Append(entry)
	}
	return out, nil
}

// IsAlias returns a bool if the specified workspace is an alias or a real account
func IsAlias(wid string) (bool, error) {
	row := dbConn.QueryRow(`SELECT alias FROM aliases WHERE wid=$1`, wid)

	var alias string
	err := row.Scan(&alias)

	switch err {
	case sql.ErrNoRows:
		break
	case nil:
		return true, nil
	case err.(*pq.Error):
		fmt.Println("Server encountered PostgreSQL error ", err)
		return false, err
	default:
		return false, err
	}
	return false, nil
}
