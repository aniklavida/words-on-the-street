package keychain

import (
	"encoding/json"
	"os"
	"sync"

	"github.com/zalando/go-keyring"
)

const (
	// ServiceName is the service name used for words-on-the-street credentials in the OS keychain.
	ServiceName = "words-on-the-street"

	// LinkedInAccount is the account identifier for the LinkedIn session cookie in the keychain.
	LinkedInAccount = "linkedin"

	// TwitterAccount is the account identifier for the Twitter session cookie in the keychain.
	TwitterAccount = "twitter"

	// TestKeychainEnv names the environment variable that can point to a file-based mock
	// keychain for headless CI environments or integration tests. Production runs leave
	// this unset to use the native OS keychain.
	TestKeychainEnv = "WORDS_ON_THE_STREET_TEST_KEYCHAIN"
)

// ErrNotFound is returned when a secret is not present in the keychain.
var ErrNotFound = keyring.ErrNotFound

// Provider defines the interface for storing and retrieving credentials from a keychain.
type Provider interface {
	Get(service, user string) (string, error)
	Set(service, user, secret string) error
	Delete(service, user string) error
}

type osProvider struct{}

func (osProvider) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (osProvider) Set(service, user, secret string) error {
	return keyring.Set(service, user, secret)
}

func (osProvider) Delete(service, user string) error {
	return keyring.Delete(service, user)
}

// MemoryProvider is an in-memory keychain provider suitable for unit tests.
type MemoryProvider struct {
	mu      sync.RWMutex
	storage map[string]string
}

// NewMemoryProvider returns an empty in-memory keychain provider.
func NewMemoryProvider() *MemoryProvider {
	return &MemoryProvider{
		storage: make(map[string]string),
	}
}

func (m *MemoryProvider) key(service, user string) string {
	return service + "::" + user
}

func (m *MemoryProvider) Get(service, user string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	val, ok := m.storage[m.key(service, user)]
	if !ok || val == "" {
		return "", ErrNotFound
	}
	return val, nil
}

func (m *MemoryProvider) Set(service, user, secret string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.storage[m.key(service, user)] = secret
	return nil
}

func (m *MemoryProvider) Delete(service, user string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.storage, m.key(service, user))
	return nil
}

// FileProvider is a file-backed keychain provider used for CLI tests in headless environments.
type FileProvider struct {
	mu   sync.Mutex
	path string
}

// NewFileProvider returns a file-backed keychain provider at the given path.
func NewFileProvider(path string) *FileProvider {
	return &FileProvider{path: path}
}

func (f *FileProvider) load() (map[string]string, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]string), nil
		}
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return make(map[string]string), nil
	}
	return m, nil
}

func (f *FileProvider) save(m map[string]string) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(f.path, data, 0600)
}

func (f *FileProvider) Get(service, user string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return "", err
	}
	val, ok := m[service+"::"+user]
	if !ok || val == "" {
		return "", ErrNotFound
	}
	return val, nil
}

func (f *FileProvider) Set(service, user, secret string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	m[service+"::"+user] = secret
	return f.save(m)
}

func (f *FileProvider) Delete(service, user string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	delete(m, service+"::"+user)
	return f.save(m)
}

var (
	providerMu     sync.RWMutex
	customProvider Provider
)

// SetProvider overrides the active keychain provider (for testing).
func SetProvider(p Provider) {
	providerMu.Lock()
	defer providerMu.Unlock()
	customProvider = p
}

// ResetProvider resets the active keychain provider to default.
func ResetProvider() {
	providerMu.Lock()
	defer providerMu.Unlock()
	customProvider = nil
}

func currentProvider() Provider {
	providerMu.RLock()
	if customProvider != nil {
		p := customProvider
		providerMu.RUnlock()
		return p
	}
	providerMu.RUnlock()

	if testPath := os.Getenv(TestKeychainEnv); testPath != "" {
		return NewFileProvider(testPath)
	}
	return osProvider{}
}

// Get retrieves a secret from the keychain.
func Get(service, user string) (string, error) {
	return currentProvider().Get(service, user)
}

// Set stores a secret in the keychain.
func Set(service, user, secret string) error {
	return currentProvider().Set(service, user, secret)
}

// Delete removes a secret from the keychain.
func Delete(service, user string) error {
	return currentProvider().Delete(service, user)
}

// LinkedInCookie returns the LinkedIn session cookie from the keychain.
func LinkedInCookie() (string, error) {
	return Get(ServiceName, LinkedInAccount)
}

// SetLinkedInCookie stores the LinkedIn session cookie in the keychain.
func SetLinkedInCookie(cookie string) error {
	return Set(ServiceName, LinkedInAccount, cookie)
}

// DeleteLinkedInCookie removes the LinkedIn session cookie from the keychain.
func DeleteLinkedInCookie() error {
	return Delete(ServiceName, LinkedInAccount)
}

// TwitterCookie returns the Twitter session cookie from the keychain.
func TwitterCookie() (string, error) {
	return Get(ServiceName, TwitterAccount)
}

// SetTwitterCookie stores the Twitter session cookie in the keychain.
func SetTwitterCookie(cookie string) error {
	return Set(ServiceName, TwitterAccount, cookie)
}

// DeleteTwitterCookie removes the Twitter session cookie from the keychain.
func DeleteTwitterCookie() error {
	return Delete(ServiceName, TwitterAccount)
}
