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
	"strconv"
	"strings"
	"time"

	"github.com/darkwyrm/anselusd/dbhandler"
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
	Tokens           []string
	LoginState       loginStatus
	IsTerminating    bool
	WID              string
	WorkspaceStatus  string
}

// Request is for encapsulating requests from the client.
type Request struct {
	Action string
	Data   map[string]string
}

// Response is for encapsulating messages to the client. We use the request-response paradigm, so
// all messages will actually be responses. All responses require a message code and accompanying
// status string.
type Response struct {
	Code   int
	Status string
	Data   map[string]string
}

// GetRequest reads a request from a client from the socket
func (s *sessionState) GetRequest() (Request, error) {
	var out Request
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
func (s sessionState) SendResponse(msg Response) (err error) {
	out, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	_, err = s.Connection.Write([]byte(out))
	return nil
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

func setupConfig() *os.File {
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
	viper.SetDefault("global.registration_subnet", "192.168.0.0/16, 172.16.0.0/12, 10.0.0.0/8")
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

	// Is a matching session key required for a device to have access?
	viper.SetDefault("security.device_checking", "on")

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
			panic(err)
		}
	}

	logHandle, err := os.OpenFile(logLocation, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("Unable to open log file %s. Aborting.\n", logLocation)
		fmt.Printf("Error: %s\n", err)
		os.Exit(1)
	}
	defer logHandle.Close()
	ServerLog = log.New(logHandle, "anselusd:", log.LstdFlags)

	_, err = os.Stat(viper.GetString("global.workspace_dir"))
	if os.IsNotExist(err) {
		err = os.Mkdir(viper.GetString("global.workspace_dir"), 0600)
		if err != nil {
			panic(err)
		}
	}

	if viper.GetString("database.password") == "" {
		ServerLog.Println("Database password not set in config file. Exiting.")
		fmt.Println("Database password not set in config file. Exiting.")
		os.Exit(1)
	}

	switch viper.GetString("global.registration") {
	case "private", "public", "network", "moderated":
		// Do nothing. Legitimate values.
	default:
		ServerLog.Println("Invalid registration mode in config file. Exiting.")
		fmt.Printf("Invalid registration mode '%s'in config file. Exiting.\n",
			viper.GetString("global.registration"))
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
		ServerLog.Println("Invalid word list in config file. Exiting.")
		fmt.Printf("Invalid word list in config file. Exiting.\n")
		os.Exit(1)
	}

	if viper.GetInt("global.registration_wordcount") < 0 ||
		viper.GetInt("global.registration_wordcount") > 12 {
		viper.Set("global.registration_wordcount", 0)
		ServerLog.Println("Registration wordcount out of bounds in config file. Assuming 6.")
		fmt.Println("Registration wordcount out of bounds in config file. Assuming 6.")
	}

	if viper.GetInt("global.default_quota") < 0 {
		viper.Set("global.default_quota", 0)
		ServerLog.Println("Negative quota value in config file. Assuming zero.")
		fmt.Println("Negative quota value in config file. Assuming zero.")
	}

	if viper.GetInt("security.failure_delay_sec") > 60 {
		viper.Set("security.failure_delay_sec", 60)
		ServerLog.Println("Limiting maximum failure delay to 60.")
		fmt.Println("Limiting maximum failure delay to 60.")
	}

	if viper.GetInt("security.max_failures") < 1 {
		viper.Set("security.max_failures", 1)
		ServerLog.Println("Invalid login failure maximum. Setting to 1.")
		fmt.Println("Invalid login failure maximum. Setting to 1.")
	} else if viper.GetInt("security.max_failures") > 10 {
		viper.Set("security.max_failures", 10)
		ServerLog.Println("Limiting login failure maximum to 10.")
		fmt.Println("Limiting login failure maximum to 10.")
	}

	if viper.GetInt("security.lockout_delay_min") < 0 {
		viper.Set("security.lockout_delay_min", 0)
		ServerLog.Println("Negative login failure lockout time. Setting to zero.")
		fmt.Println("Negative login failure lockout time. Setting to zero.")
	}

	if viper.GetInt("security.registration_delay_min") < 0 {
		viper.Set("security.registration_delay_min", 0)
		ServerLog.Println("Negative registration delay. Setting to zero.")
		fmt.Println("Negative registration delay. Setting to zero.")
	}

	devChecking := strings.ToLower(viper.GetString("security.device_checking"))
	if devChecking != "on" && devChecking != "off" {
		viper.Set("security.devChecking", "on")
		ServerLog.Println("Invalid device checking value. Exiting.")
		fmt.Println("Invalid device checking value. Exiting.")
		os.Exit(1)
	}

	return logHandle
}

func main() {
	logHandle := setupConfig()
	defer logHandle.Close()

	dbhandler.Connect(ServerLog)
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

	pattern := regexp.MustCompile("\"[^\"]+\"|\"[^\"]+$|[\\S\\[\\]]+")

	session.WriteClient("{'name':'Anselus','version':'0.1','code':200,'status':'OK'}\r\n")
	for {
		clientString, err := session.ReadClient()
		if err != nil && err.Error() != "EOF" {
			break
		}
		session.Tokens = pattern.FindAllString(clientString, -1)

		if len(session.Tokens) > 0 {
			if session.Tokens[0] == "QUIT" {
				break
			}
			processCommand(&session)
		}
		if session.IsTerminating {
			break
		}
		conn.SetReadDeadline(time.Now().Add(time.Minute * 30))
		conn.SetWriteDeadline(time.Now().Add(time.Minute * 10))
	}
}

func processCommand(session *sessionState) {
	switch session.Tokens[0] {
	/*
		Commands to Implement:
		COPY
		DELETE
		DELIVER
		DOWNLOAD
		GETENTRIES
		GETUPDATES
		ISCURRENT
		LIST
		MKDIR
		MOVE
		RESUME
		SELECT
		SEND
		SERVERID
		SERVERPWD
		SETADDR
		UNREGISTER
		UPLOAD
		USERCARD
	*/
	case "DEVICE":
		commandDevice(session)
	case "EXISTS":
		commandExists(session)
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
	default:
		commandUnrecognized(session)
	}
}

// setupUpload is for ensuring the synchronous transfer of data
func setupUpload(session *sessionState, byteCount uint64) (uint64, error) {
	if byteCount == 0 {
		return 0, nil
	}
	_, err := session.WriteClient(fmt.Sprintf("104 TRANSFER %d\r\n", byteCount))
	if err != nil {
		return 0, err
	}

	response, err := session.ReadClient()
	if err != nil {
		return 0, err
	}

	pattern := regexp.MustCompile("^\\d{3}( [[:alnum:]]+)+")
	if !pattern.MatchString(response) {
		return 0, errors.New("malformed client response")
	}

	code, err := strconv.Atoi(response[0:3])
	if err != nil {
		return 0, err
	}

	if code != 104 || response[0:13] != "104 TRANSFER " {
		return 0, errors.New(response)
	}

	parts := strings.Split(response, " ")
	clientCount, err := strconv.ParseUint(parts[2], 10, 64)

	return clientCount, nil
}

func commandExists(session *sessionState) {
	// Command syntax:
	// EXISTS <path>

	if session.LoginState != loginClientSession {
		session.WriteClient("401 UNAUTHORIZED\r\n")
		return
	}

	if len(session.Tokens) < 2 {
		session.WriteClient("400 BAD REQUEST\r\n")
		return
	}

	fsPath := filepath.Join(viper.GetString("global.workspace_dir"), session.WID,
		strings.Join(session.Tokens[1:], string(os.PathSeparator)))
	_, err := os.Stat(fsPath)
	if err != nil {
		if os.IsNotExist(err) {
			session.WriteClient("404 NOT FOUND\r\n")
		} else {
			session.WriteClient("300 INTERNAL SERVER ERROR\r\n")
		}
	} else {
		session.WriteClient("200 OK\r\n")
	}
}
