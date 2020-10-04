package keycard

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"time"

	"github.com/darkwyrm/b85"
	"github.com/darkwyrm/gostringlist"
	"github.com/zeebo/blake3"
	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/nacl/auth"
	"golang.org/x/crypto/nacl/sign"
	"golang.org/x/crypto/sha3"
)

// AlgoString encapsulates a Base85-encoded binary string and its associated algorithm.
// Algorithms are expected to utilize capital letters, dashes, and numbers and be no more than
// 16 characters, not including the colon separator.
// Example: ED25519:p;XXU0XF#UO^}vKbC-wS(#5W6=OEIFmR2z`rS1j+
type AlgoString struct {
	Prefix string
	Data   string
}

// Set assigns an AlgoString-formatted string to the object
func (as AlgoString) Set(data string) error {
	if len(data) < 1 {
		as.Prefix = ""
		as.Data = ""
		return nil
	}

	parts := strings.SplitN(data, ":", 1)
	if len(parts) != 2 {
		return errors.New("bad string format")
	}
	as.Prefix = parts[0]
	as.Data = parts[1]

	return nil
}

// SetBytes initializes the AlgoString from an array of bytes
func (as AlgoString) SetBytes(data []byte) error {
	return as.Set(string(data))
}

// AsBytes returns the AlgoString as a byte array
func (as AlgoString) AsBytes() []byte {
	return []byte(as.Prefix + ":" + as.Data)
}

// AsString returns the AlgoString as a complete string
func (as AlgoString) AsString() string {
	return as.Prefix + ":" + as.Data
}

// IsValid returns true if the object contains valid data
func (as AlgoString) IsValid() bool {
	return (len(as.Prefix) > 0 && len(as.Data) > 0)
}

// RawData returns the raw data held in the string
func (as AlgoString) RawData() ([]byte, error) {
	return b85.Decode(as.Data)
}

// MakeEmpty clears the AlgoString's internal data
func (as AlgoString) MakeEmpty() {
	as.Prefix = ""
	as.Data = ""
}

// SigInfo contains descriptive information about the signatures for an entry. The Level property
// indicates order. For example, a signature with a level of 2 is attached to the entry after a
// level 1 signature.
type SigInfo struct {
	Name     string
	Level    int
	Optional bool
	Type     uint8
}

// SigInfoHash - signature field is a hash
const SigInfoHash uint8 = 1

// SigInfoSignature - signature field is a cryptographic signature
const SigInfoSignature uint8 = 2

// SigInfoList is a specialized list container for SigInfo structure instances
type SigInfoList struct {
	Items []SigInfo
}

// Contains returns true if one of the SigInfo items has the specified name
func (sil SigInfoList) Contains(name string) bool {
	for _, item := range sil.Items {
		if item.Name == name {
			return true
		}
	}
	return false
}

// IndexOf returns the index of the item named and -1 if it doesn't exist
func (sil SigInfoList) IndexOf(name string) int {
	for i, item := range sil.Items {
		if item.Name == name {
			return i
		}
	}
	return -1
}

// GetItem returns the item matching the specified name or nil if it doesn't exist
func (sil SigInfoList) GetItem(name string) (bool, *SigInfo) {
	for _, item := range sil.Items {
		if item.Name == name {
			return true, &item
		}
	}
	var empty SigInfo
	return false, &empty
}

// EntryBase contains the common functionality for keycard entries
type EntryBase struct {
	Type           string
	Fields         map[string]string
	FieldNames     gostringlist.StringList
	RequiredFields gostringlist.StringList
	Signatures     map[string]string
	SignatureInfo  SigInfoList
	PrevHash       string
	Hash           string
}

// IsCompliant returns true if the object meets spec compliance (required fields, etc.)
func (eb EntryBase) IsCompliant() bool {
	if eb.Type != "User" && eb.Type != "Organization" {
		return false
	}

	// Field compliance
	for _, reqField := range eb.RequiredFields.Items {
		_, ok := eb.Fields[reqField]
		if !ok {
			return false
		}
	}

	// Signature compliance
	for _, item := range eb.SignatureInfo.Items {
		if item.Type == SigInfoHash {
			if len(eb.Hash) < 1 {
				return false
			}
			continue
		}

		if item.Type != SigInfoSignature {
			return false
		}

		if item.Optional {
			val, err := eb.Signatures[item.Name]
			if err || len(val) < 1 {
				return false
			}

		}
	}

	return true
}

// GetSignature - get the specified signature
func (eb EntryBase) GetSignature(sigtype string) (string, error) {
	val, exists := eb.Signatures[sigtype]
	if exists {
		return val, nil
	}
	return val, errors.New("signature not found")
}

// MakeByteString converts the entry to a string of bytes to ensure that signatures are not
// invalidated by automatic line ending handling
func (eb EntryBase) MakeByteString(siglevel int) []byte {

	// Capacity is all possible field names + all actual signatures + hash fields
	lines := make([][]byte, 0, len(eb.FieldNames.Items)+len(eb.Signatures)+2)
	if len(eb.Type) > 0 {
		lines = append(lines, []byte(eb.Type))
	}

	for _, fieldName := range eb.FieldNames.Items {
		if len(eb.Fields[fieldName]) > 0 {
			lines = append(lines, []byte(fieldName+":"+eb.Fields[fieldName]))
		}
	}

	if siglevel < 0 || siglevel > len(eb.SignatureInfo.Items) {
		siglevel = eb.SignatureInfo.Items[len(eb.SignatureInfo.Items)-1].Level
	}

	for i := 0; i < siglevel; i++ {
		if eb.SignatureInfo.Items[i].Type == SigInfoHash {
			if len(eb.PrevHash) > 0 {
				lines = append(lines, []byte("Previous-Hash:"+eb.PrevHash))
			}
			if len(eb.Hash) > 0 {
				lines = append(lines, []byte("Hash:"+eb.Hash))
			}
			continue
		}

		if eb.SignatureInfo.Items[i].Type != SigInfoSignature {
			panic("BUG: invalid signature info type in EntryBase.MakeByteString")
		}

		val, ok := eb.Signatures[eb.SignatureInfo.Items[i].Name]
		if ok && len(val) > 0 {
			lines = append(lines, []byte(eb.SignatureInfo.Items[i].Name+"-Signature:"+val))
		}

	}

	return bytes.Join(lines, []byte("\r\n"))
}

// Save saves the entry to disk
func (eb EntryBase) Save(path string, clobber bool) error {
	if len(path) < 1 {
		return errors.New("empty path")
	}

	_, err := os.Stat(path)
	if !os.IsNotExist(err) && !clobber {
		return errors.New("file exists")
	}

	return ioutil.WriteFile(path, eb.MakeByteString(-1), 0644)
}

// SetField sets an entry field to the specified value.
func (eb EntryBase) SetField(fieldName string, fieldValue string) error {
	if len(fieldName) < 1 {
		return errors.New("empty field name")
	}
	eb.Fields[fieldName] = fieldValue

	// Any kind of editing invalidates the signatures and hashes
	eb.Signatures = make(map[string]string)
	return nil
}

// SetFields sets multiple entry fields
func (eb EntryBase) SetFields(fields map[string]string) {
	// Any kind of editing invalidates the signatures and hashes. Unlike SetField, we clear the
	// signature fields first because it's possible to set everything in the entry with this
	// method, so the signatures can be valid after the call finishes if they are set by the
	// caller.
	eb.Signatures = make(map[string]string)

	for k, v := range fields {
		eb.Fields[k] = v
	}
}

// Set initializes the entry from a bytestring
func (eb EntryBase) Set(data []byte) error {
	if len(data) < 1 {
		return errors.New("empty byte field")
	}

	lines := strings.Split(string(data), "\r\n")

	for linenum, rawline := range lines {
		line := strings.TrimSpace(rawline)
		parts := strings.SplitN(line, ":", 1)

		if len(parts) != 2 {
			return fmt.Errorf("bad data near line %d", linenum)
		}

		if parts[0] == "Type" {
			if parts[1] != eb.Type {
				return fmt.Errorf("Can't use %s data on %s entries", parts[1], eb.Type)
			}
		} else if strings.HasSuffix(parts[0], "Signature") {
			sigparts := strings.SplitN(parts[0], "-", 1)
			if !eb.SignatureInfo.Contains(sigparts[0]) {
				return fmt.Errorf("%s is not a valid signature type", sigparts[0])
			}
			eb.Signatures[sigparts[0]] = sigparts[1]
		} else {
			eb.Fields[parts[0]] = parts[1]
		}
	}

	return nil
}

// SetExpiration enables custom expiration dates, the standard being 90 days for user entries and
// 1 year for organizations.
func (eb EntryBase) SetExpiration(numdays int16) error {
	if numdays < 0 {
		if eb.Type == "Organization" {
			numdays = 365
		} else if eb.Type == "User" {
			numdays = 90
		} else {
			return errors.New("unsupported keycard type")
		}
	}

	// An expiration date can be no longer than three years
	if numdays > 1095 {
		numdays = 1095
	}

	eb.Fields["Expiration"] = time.Now().AddDate(0, 0, int(numdays)).Format("%Y%m%d")

	return nil
}

// Sign cryptographically signs an entry. The supported types and expected order of the signature
// is defined by subclasses using the SigInfo instances in the object's SignatureInfo property.
// Adding a particular signature causes those that must follow it to be cleared. The EntryBase's
// cryptographic hash counts as a signature in this matter. Thus, if an Organization signature is
// added to the entry, the instance's hash and User signatures are both cleared.
func (eb EntryBase) Sign(signingKey AlgoString, sigtype string) error {
	if !signingKey.IsValid() {
		return errors.New("bad signing key")
	}

	if signingKey.Prefix != "ED25519" {
		return errors.New("unsupported signing algorithm")
	}

	sigtypeOK := false
	sigtypeIndex := -1
	for i := range eb.SignatureInfo.Items {
		if sigtype == eb.SignatureInfo.Items[i].Name {
			sigtypeOK = true
			sigtypeIndex = i
		}

		// Once we have found the index of the signature, it and all following signatures must be
		// cleared because they will no longer be valid
		if sigtypeOK {
			eb.Signatures[eb.SignatureInfo.Items[i].Name] = ""
		}
	}

	if !sigtypeOK {
		return errors.New("bad signature type")
	}

	signkeyDecoded, err := signingKey.RawData()
	if err != nil {
		return err
	}

	var signkeyArray [64]byte
	signKeyAdapter := signkeyArray[0:64]
	copy(signKeyAdapter, signkeyDecoded)

	signature := sign.Sign(nil, eb.MakeByteString(sigtypeIndex+1), &signkeyArray)
	eb.Signatures[sigtype] = "ED25519:" + b85.Encode(signature)

	return nil
}

// GenerateHash generates a hash containing the expected signatures and the previous hash, if it
// exists. The supported hash algorithms are 'BLAKE3-256', 'BLAKE2', 'SHA-256', and 'SHA3-256'.
func (eb EntryBase) GenerateHash(algorithm string) error {
	validAlgorithm := false
	switch algorithm {
	case
		"BLAKE3-256",
		"BLAKE2",
		"SHA-256",
		"SHA3-256":
		validAlgorithm = true
	}

	if !validAlgorithm {
		return errors.New("unsupported hashing algorithm")
	}

	hashLevel := -1
	for i := range eb.SignatureInfo.Items {
		if eb.SignatureInfo.Items[i].Type == SigInfoHash {
			hashLevel = eb.SignatureInfo.Items[i].Level
			break
		}
	}

	if hashLevel < 0 {
		panic("BUG: SignatureInfo missing hash entry")
	}

	switch algorithm {
	case "BLAKE3-256":
		hasher := blake3.New()
		sum := hasher.Sum(eb.MakeByteString(hashLevel))
		eb.Hash = algorithm + b85.Encode(sum[:])
	case "BLAKE2":
		sum := blake2b.Sum256(eb.MakeByteString(hashLevel))
		eb.Hash = algorithm + b85.Encode(sum[:])
	case "SHA256":
		sum := sha256.Sum256(eb.MakeByteString(hashLevel))
		eb.Hash = algorithm + b85.Encode(sum[:])
	case "SHA3-256":
		sum := sha3.Sum256(eb.MakeByteString(hashLevel))
		eb.Hash = algorithm + b85.Encode(sum[:])
	}

	return nil
}

// VerifySignature cryptographically verifies the entry against the key provided, given the
// specific signature to verify.
func (eb EntryBase) VerifySignature(verifyKey AlgoString, sigtype string) (bool, error) {

	if !verifyKey.IsValid() {
		return false, errors.New("bad verification key")
	}

	if verifyKey.Prefix != "ED25519" {
		return false, errors.New("unsupported signing algorithm")
	}

	if !eb.SignatureInfo.Contains(sigtype) {
		return false, fmt.Errorf("%s is not a valid signature type", sigtype)
	}

	infoValid, sigInfo := eb.SignatureInfo.GetItem(sigtype)
	if !infoValid {
		return false, errors.New("specified signature missing")
	}

	if eb.Signatures[sigtype] == "" {
		return false, errors.New("specified signature empty")
	}

	var sig AlgoString
	err := sig.Set(eb.Signatures[sigtype])
	if err != nil {
		return false, err
	}
	if sig.Prefix != "ED25519" {
		return false, errors.New("signature uses unsupported signing algorithm")
	}

	verifykeyDecoded, err := verifyKey.RawData()
	if err != nil {
		return false, err
	}

	var verifykeyArray [32]byte
	verifyKeyAdapter := verifykeyArray[0:32]
	copy(verifyKeyAdapter, verifykeyDecoded)

	digest, err := sig.RawData()
	if err != nil {
		return false, errors.New("decoding error in signature")
	}
	verifyStatus := auth.Verify(digest, eb.MakeByteString(sigInfo.Level), &verifykeyArray)

	return verifyStatus, nil
}

// OrgEntry - a class to represent organization keycard entries
type OrgEntry struct {
	EntryBase
}

// NewOrgEntry creates a new OrgEntry
func NewOrgEntry() *OrgEntry {
	self := new(OrgEntry)

	self.Type = "Organization"
	self.FieldNames.Items = []string{
		"Index",
		"Name",
		"Contact-Admin",
		"Contact-Abuse",
		"Contact-Support",
		"Language",
		"Primary-Signing-Key",
		"Secondary-Signing-Key",
		"Encryption-Key",
		"Time-To-Live",
		"Expires"}

	self.RequiredFields.Items = []string{
		"Index",
		"Name",
		"Contact-Admin",
		"Primary-Signing-Key",
		"Encryption-Key",
		"Time-To-Live",
		"Expires"}

	self.SignatureInfo.Items = []SigInfo{
		SigInfo{"Custody", 1, true, SigInfoSignature},
		SigInfo{"Organization", 2, false, SigInfoSignature},
		SigInfo{"Hashes", 3, false, SigInfoHash}}

	self.Fields["Index"] = "1"
	self.Fields["Time-To-Live"] = "30"
	self.SetExpiration(-1)

	return self
}

// getStringMapKeys returns a StringList containing the keys in a map[string]string
func getStringMapKeys(data map[string]string) gostringlist.StringList {
	var out gostringlist.StringList
	out.Items = make([]string, len(data))
	i := 0
	for k := range data {
		out.Items[i] = k
		i++
	}
	return out
}
