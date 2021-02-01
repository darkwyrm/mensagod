package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/darkwyrm/anselusd/dbhandler"
	"github.com/darkwyrm/anselusd/logging"
	"github.com/everlastingbeta/diceware"
	"github.com/everlastingbeta/diceware/wordlist"
	_ "github.com/lib/pq"
	"github.com/spf13/viper"
)

// ServerLog is the global logging object
var ServerLog *log.Logger

// gRegWordList is a copy of the word list for preregistration code generation
var gRegWordList diceware.Wordlist

// -------------------------------------------------------------------------------------------
// Types
// -------------------------------------------------------------------------------------------

// MaxCommandLength is the maximum number of bytes an Anselus command is permitted to be, including
// end-of-line terminator. Note that bulk transfers are not subject to this restriction -- just the
// initial command.
const MaxCommandLength = 1024

type loginStatus int

const (
	// Unauthenticated state
	loginNoSession loginStatus = iota
	// Client has requested a valid workspace. Awaiting password.
	loginAwaitingPassword
	// Client has submitted a valid password. Awaiting session ID.
	loginAwaitingSessionID
	// Client has successfully authenticated
	loginClientSession
)

type sessionState struct {
	PasswordFailures int
	Connection       net.Conn
	Message          ClientRequest
	LoginState       loginStatus
	IsTerminating    bool
	WID              string
	WorkspaceStatus  string
}

// ClientRequest is for encapsulating requests from the client.
type ClientRequest struct {
	Action string
	Data   map[string]string
}

// ServerResponse is for encapsulating messages to the client. We use the request-response paradigm,
// so all messages will actually be responses. All responses require a message code and accompanying
// status string.
type ServerResponse struct {
	Code   int
	Status string
	Info   string
	Data   map[string]string
}

// NewServerResponse creates a new server response which is fully initialized and ready to use
func NewServerResponse(code int, status string) *ServerResponse {
	var r ServerResponse
	r.Code = code
	r.Status = status
	r.Data = make(map[string]string)
	return &r
}

// HasField is syntactic sugar for checking if a request contains a particular field.
func (r *ClientRequest) HasField(fieldname string) bool {
	_, exists := r.Data[fieldname]
	return exists
}

// Validate performs schema validation for the request. Given a slice of strings containing the
// required Data keys, it returns an error if any of them are missing. While HasField() can be
// used to accomplish the same task, Validate() is for ensuring that all required data fields in
// a client request exist in one call.
func (r *ClientRequest) Validate(fieldlist []string) error {
	for _, fieldname := range fieldlist {
		_, exists := r.Data[fieldname]
		if !exists {
			return fmt.Errorf("missing field %s", fieldname)
		}
	}
	return nil
}

// GetRequest reads a request from a client from the socket
func (s *sessionState) GetRequest() (ClientRequest, error) {
	var out ClientRequest
	buffer := make([]byte, MaxCommandLength)
	bytesRead, err := s.Connection.Read(buffer)
	if err != nil {
		ne, ok := err.(*net.OpError)
		if ok && ne.Timeout() {
			s.IsTerminating = true
			return out, errors.New("connection timed out")
		}

		if err.Error() != "EOF" {
			fmt.Println("Error reading from client: ", err.Error())
		}
		return out, err
	}

	err = json.Unmarshal(buffer[:bytesRead], &out)

	return out, nil
}

// SendResponse sends a JSON response message to the client
func (s sessionState) SendResponse(msg ServerResponse) (err error) {
	out, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	_, err = s.Connection.Write([]byte(out))
	return nil
}

// SendStringResponse is a syntactic sugar command for quickly sending error responses. The Info
// field can contain additional information related to the return code
func (s sessionState) SendStringResponse(code int, status string, info string) (err error) {
	return s.SendResponse(ServerResponse{code, status, info, map[string]string{}})
}

func (s *sessionState) ReadClient() (string, error) {
	buffer := make([]byte, MaxCommandLength)
	bytesRead, err := s.Connection.Read(buffer)
	if err != nil {
		ne, ok := err.(*net.OpError)
		if ok && ne.Timeout() {
			s.IsTerminating = true
			return "", errors.New("connection timed out")
		}

		if err.Error() != "EOF" {
			fmt.Println("Error reading from client: ", err.Error())
		}
		return "", err
	}

	return strings.TrimSpace(string(buffer[:bytesRead])), nil
}

func (s sessionState) WriteClient(msg string) (n int, err error) {
	return s.Connection.Write([]byte(msg))
}

// -------------------------------------------------------------------------------------------
// Function Definitions
// -------------------------------------------------------------------------------------------

func setupConfig() {
	// IP and port to listen on
	viper.SetDefault("network.listen_ip", "127.0.0.1")
	viper.SetDefault("network.port", "2001")

	// Database config
	viper.SetDefault("database.engine", "postgresql")
	viper.SetDefault("database.ip", "127.0.0.1")
	viper.SetDefault("database.port", "5432")
	viper.SetDefault("database.name", "anselus")
	viper.SetDefault("database.user", "anselus")
	viper.SetDefault("database.password", "")

	// Location of workspace data, server log
	switch runtime.GOOS {
	case "js", "nacl":
		fmt.Println("Javascript and NaCl are not supported platforms for Anselus Server.")
		os.Exit(1)
	case "windows":
		programData, success := os.LookupEnv("ProgramData")
		if !success {
			programData = "C:\\ProgramData"
		}

		viper.SetDefault("global.workspace_dir", filepath.Join(programData, "anselus"))
		viper.Set("global.log_dir", filepath.Join(programData, "anselusd"))
		viper.SetConfigName("serverconfig")
		viper.AddConfigPath(filepath.Join(programData, "anselusd"))
	default:
		viper.SetDefault("global.workspace_dir", "/var/anselus/")
		viper.Set("global.log_dir", "/var/log/anselusd/")
		viper.SetConfigName("serverconfig")
		viper.AddConfigPath("/etc/anselusd/")
	}

	// Account registration modes
	// public - Outside registration requests.
	// network - registration is public, but restricted to a subnet or single IP address
	// moderated - A registration request is sent and a moderator must approve the account
	//			   prior to its creation
	// private - an account can be created only by an administrator -- outside requests will bounce
	viper.SetDefault("global.registration", "private")

	// Subnet(s) used for network registration. Defaults to private networks only.
	viper.SetDefault("global.registration_subnet",
		"192.168.0.0/16, 172.16.0.0/12, 10.0.0.0/8, 127.0.0.1/8")
	viper.SetDefault("global.registration_subnet6", "fe80::/10")
	viper.SetDefault("global.registration_wordlist", "eff_short_prefix")
	viper.SetDefault("global.registration_wordcount", 6)

	// Default user workspace quota in MiB. 0 = no quota
	viper.SetDefault("global.default_quota", 0)

	// Delay after an unsuccessful login
	viper.SetDefault("security.failure_delay_sec", 3)

	// Max number of login failures before the connection is closed
	viper.SetDefault("security.max_failures", 5)

	// Lockout time (in minutes) after max_failures exceeded
	viper.SetDefault("security.lockout_delay_min", 15)

	// Delay (in minutes) the number of minutes which must pass before another account registration
	// can be requested from the same IP address -- for preventing registration spam/DoS.
	viper.SetDefault("security.registration_delay_min", 15)

	// Resource usage for password hashing
	viper.SetDefault("security.password_security", "normal")

	// Read the config file
	err := viper.ReadInConfig()
	if err != nil {
		fmt.Printf("Unable to locate config file. Exiting. Error: %s", err)
		os.Exit(1)
	}

	logLocation := filepath.Join(viper.GetString("global.log_dir"), "anselusd.log")
	if _, err := os.Stat(viper.GetString("global.log_dir")); os.IsNotExist(err) {
		err = os.Mkdir(viper.GetString("global.log_dir"), 0600)
		if err != nil {
			fmt.Printf("Unable to create log directory %s. Exiting. Error: %s",
				viper.GetString("global.log_dir"), err)
			os.Exit(1)
		}
	}

	logging.Init(logLocation, true)

	_, err = os.Stat(viper.GetString("global.workspace_dir"))
	if os.IsNotExist(err) {
		err = os.Mkdir(viper.GetString("global.workspace_dir"), 0600)
		if err != nil {
			fmt.Printf("Unable to create workspace directory %s. Exiting. Error: %s",
				viper.GetString("global.workspace_dir"), err)
			os.Exit(1)
		}
	}

	if viper.GetString("database.password") == "" {
		logging.Write("Database password not set in config file. Exiting.")
		logging.Shutdown()
		os.Exit(1)
	}

	pattern := regexp.MustCompile("([a-zA-Z0-9]+\x2E)+[a-zA-Z0-9]+")
	if viper.GetString("global.domain") == "" ||
		!pattern.MatchString(viper.GetString("global.domain")) {
		logging.Write("Missing or invalid domain in config file. Exiting.")
		logging.Shutdown()
		os.Exit(1)
	}

	switch viper.GetString("global.registration") {
	case "private", "public", "network", "moderated":
		// Do nothing. Legitimate values.
	default:
		logging.Write("Invalid registration mode in config file. Exiting.")
		logging.Shutdown()
		os.Exit(1)
	}

	wordList := viper.GetString("global.registration_wordlist")
	switch wordList {
	case "eff_short":
		gRegWordList = wordlist.EFFShort
	case "eff_short_prefix":
		gRegWordList = wordlist.EFFShortPrefix
	case "eff_long":
		gRegWordList = wordlist.EFFLong
	case "original":
		gRegWordList = wordlist.Original
	default:
		logging.Write("Invalid word list in config file. Exiting.")
		logging.Shutdown()
		os.Exit(1)
	}

	if viper.GetInt("global.registration_wordcount") < 0 ||
		viper.GetInt("global.registration_wordcount") > 12 {
		viper.Set("global.registration_wordcount", 0)
		logging.Write("Registration wordcount out of bounds in config file. Assuming 6.")
	}

	if viper.GetInt("global.default_quota") < 0 {
		viper.Set("global.default_quota", 0)
		logging.Write("Negative quota value in config file. Assuming zero.")
	}

	if viper.GetInt("security.failure_delay_sec") > 60 {
		viper.Set("security.failure_delay_sec", 60)
		logging.Write("Limiting maximum failure delay to 60.")
	}

	if viper.GetInt("security.max_failures") < 1 {
		viper.Set("security.max_failures", 1)
		logging.Write("Invalid login failure maximum. Setting to 1.")
	} else if viper.GetInt("security.max_failures") > 10 {
		viper.Set("security.max_failures", 10)
		logging.Write("Limiting login failure maximum to 10.")
	}

	if viper.GetInt("security.lockout_delay_min") < 0 {
		viper.Set("security.lockout_delay_min", 0)
		logging.Write("Negative login failure lockout time. Setting to zero.")
	}

	if viper.GetInt("security.registration_delay_min") < 0 {
		viper.Set("security.registration_delay_min", 0)
		logging.Write("Negative registration delay. Setting to zero.")
	}
}

func main() {
	setupConfig()

	dbhandler.Connect()
	if !dbhandler.IsConnected() {
		fmt.Println("Unable to connect to database server. Quitting.")
		os.Exit(1)
	}
	defer dbhandler.Disconnect()

	listenString := viper.GetString("network.listen_ip") + ":" + viper.GetString("network.port")
	listener, err := net.Listen("tcp", listenString)
	if err != nil {
		fmt.Println("Error setting up listener: ", err.Error())
		os.Exit(1)
	} else {
		fmt.Println("Listening on " + listenString)
	}

	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("Error accepting a connection: ", err.Error())
			os.Exit(1)
		}
		go connectionWorker(conn)
	}
}

func connectionWorker(conn net.Conn) {
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(time.Minute * 30))
	conn.SetWriteDeadline(time.Now().Add(time.Minute * 10))

	var session sessionState
	session.Connection = conn
	session.LoginState = loginNoSession

	session.WriteClient("{\"Name\":\"Anselus\",\"Version\":\"0.1\",\"Code\":200," +
		"\"Status\":\"OK\"}\r\n")
	for {
		request, err := session.GetRequest()
		if err != nil && err.Error() != "EOF" {
			break
		}
		session.Message = request

		if request.Action == "QUIT" {
			break
		}
		processCommand(&session)

		if session.IsTerminating {
			break
		}
		conn.SetReadDeadline(time.Now().Add(time.Minute * 30))
		conn.SetWriteDeadline(time.Now().Add(time.Minute * 10))
	}
}

func processCommand(session *sessionState) {
	switch session.Message.Action {
	case "ADDENTRY":
		commandAddEntry(session)
	case "CANCEL":
		commandCancel(session)
	case "DEVICE":
		commandDevice(session)
	case "EXISTS":
		commandExists(session)
	case "GETWID":
		commandGetWID(session)
	case "ISCURRENT":
		commandIsCurrent(session)
	case "LOGIN":
		commandLogin(session)
	case "LOGOUT":
		commandLogout(session)
	case "NOOP":
		// Do nothing. Just resets the idle counter.
	case "ORGCARD":
		commandOrgCard(session)
	case "PASSWORD":
		commandPassword(session)
	case "PREREG":
		commandPreregister(session)
	case "REGCODE":
		commandRegCode(session)
	case "REGISTER":
		commandRegister(session)
	case "SETPASSWORD":
		commandSetPassword(session)
	case "UNREGISTER":
		commandUnregister(session)
	case "USERCARD":
		commandUserCard(session)
	default:
		commandUnrecognized(session)
	}
}

func commandCancel(session *sessionState) {
	if session.LoginState != loginClientSession {
		session.LoginState = loginNoSession
	}
	session.SendStringResponse(200, "OK", "")
}

func commandExists(session *sessionState) {
	// Command syntax:
	// EXISTS(Path)

	if session.LoginState != loginClientSession {
		session.SendStringResponse(401, "UNAUTHORIZED", "")
		return
	}

	if !session.Message.HasField("Path") {
		session.SendStringResponse(400, "BAD REQUEST", "Missing required field")
		return
	}

	fsPath := filepath.Join(viper.GetString("global.workspace_dir"), session.WID,
		session.Message.Data["Path"])
	_, err := os.Stat(fsPath)
	if err != nil {
		if os.IsNotExist(err) {
			session.SendStringResponse(404, "NOT FOUND", "")
		} else {
			session.SendStringResponse(300, "INTERNAL SERVER ERROR", "")
			logging.Writef("commandExists: Filesystem error %s", err.Error())
		}
	} else {
		session.SendStringResponse(200, "OK", "")
	}
}
