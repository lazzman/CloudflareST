package management

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/masgzy/CloudflareST/internal/integrations"
	"github.com/masgzy/CloudflareST/internal/optimizer"
	"golang.org/x/crypto/bcrypt"
)

type Service struct {
	mu              sync.Mutex
	db              *sql.DB
	lock            *flock.Flock
	key             []byte
	aead            cipher.AEAD
	passwordHash    []byte
	passwordVersion string
	passwordSource  string
	passwordFile    string
	runtimeDir      string
	secureCookie    bool
	engine          Engine
	provider        Provider
	now             func() time.Time
	ctx             context.Context
	cancel          context.CancelFunc
	wake            chan struct{}
	wg              sync.WaitGroup
	cancels         map[string]context.CancelFunc
	targetMu        sync.Mutex
	targetLocks     map[string]*sync.Mutex
	manualBusy      int
	trustedProxies  []netip.Prefix
}

func randomID() string {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value)
}

func Open(config Config) (_ *Service, failure error) {
	var trusted []netip.Prefix
	for _, value := range config.TrustedProxies {
		if strings.TrimSpace(value) == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
		if err != nil {
			return nil, errors.New("可信代理必须使用有效 CIDR，未启用来源头信任")
		}
		trusted = append(trusted, prefix.Masked())
	}
	if config.DataDir == "" {
		return nil, errors.New("必须指定管理数据目录")
	}
	if err := os.MkdirAll(config.DataDir, 0700); err != nil {
		return nil, fmt.Errorf("无法准备管理数据目录 %q：%w", config.DataDir, err)
	}
	lock := flock.New(filepath.Join(config.DataDir, "management.lock"))
	locked, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("无法锁定管理数据目录 %q（需要写入权限和文件锁支持）：%w", config.DataDir, err)
	}
	if !locked {
		return nil, errors.New("该数据卷已有管理实例持有执行权，请停止旧实例后重试")
	}
	defer func() {
		if failure != nil {
			_ = lock.Unlock()
		}
	}()
	keyPath := filepath.Join(config.DataDir, "master.key")
	key, err := os.ReadFile(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		if info, statErr := os.Stat(filepath.Join(config.DataDir, "management.sqlite")); statErr == nil && info.Size() > 0 {
			return nil, errors.New("已有数据库但缺少主密钥，请恢复完整数据卷备份；未重新生成密钥")
		}
		key = make([]byte, 32)
		if _, err = rand.Read(key); err == nil {
			err = os.WriteFile(keyPath, key, 0600)
		}
	}
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("数据卷主密钥无效，请恢复对应备份，不要重新生成密钥")
	}
	if err = os.Chmod(keyPath, 0600); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(config.DataDir, "management.sqlite")
	file, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	_ = file.Close()
	if err = os.Chmod(dbPath, 0600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		if failure != nil {
			_ = db.Close()
		}
	}()
	db.SetMaxOpenConns(1)
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return nil, err
	}
	if version > 4 {
		return nil, errors.New("数据卷版本高于当前程序，不支持原地降级；请使用对应版本或恢复备份")
	}
	if _, err = db.Exec("PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA secure_delete=ON; PRAGMA busy_timeout=5000; CREATE TABLE IF NOT EXISTS objects (kind TEXT NOT NULL,id TEXT NOT NULL,body BLOB NOT NULL,PRIMARY KEY(kind,id));"); err != nil {
		return nil, err
	}
	if err = migrateRunStorage(db); err != nil {
		return nil, err
	}
	if err = migratePrivateHistory(db); err != nil {
		return nil, err
	}
	if version < 4 {
		if _, err = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	service := &Service{db: db, lock: lock, key: key, aead: aead, secureCookie: config.SecureCookie, engine: config.Engine, provider: config.Provider, now: config.Now, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), cancels: map[string]context.CancelFunc{}, targetLocks: map[string]*sync.Mutex{}}
	service.trustedProxies = trusted
	if service.now == nil {
		service.now = time.Now
	}
	if service.engine == nil {
		service.engine = optimizer.New()
	}
	if service.provider == nil {
		service.provider = integrations.New()
	}
	password := config.Password
	service.passwordSource = "environment"
	legacyPasswordFile := filepath.Join(config.DataDir, "admin-password")
	if err = os.Remove(legacyPasswordFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		cancel()
		return nil, err
	}
	if password == "" {
		password = randomID()
		service.passwordSource = "generated"
		service.runtimeDir = config.RuntimeDir
		if service.runtimeDir == "" {
			service.runtimeDir, err = os.MkdirTemp("", "cfst-runtime-")
		} else {
			err = os.MkdirAll(service.runtimeDir, 0700)
		}
		if err != nil {
			cancel()
			return nil, err
		}
		if err = os.Chmod(service.runtimeDir, 0700); err != nil {
			cancel()
			return nil, err
		}
		service.passwordFile = filepath.Join(service.runtimeDir, "admin-password")
		if err = os.WriteFile(service.passwordFile, []byte(password+"\n"), 0600); err != nil {
			cancel()
			return nil, err
		}
		if err = os.Chmod(service.passwordFile, 0600); err != nil {
			cancel()
			return nil, err
		}
	}
	service.passwordVersion = service.digest([]byte(password))
	service.passwordHash, err = bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		cancel()
		return nil, errors.New("管理密码无法初始化，长度不得超过72字节")
	}
	if _, err = get[Settings](db, "settings", "system"); errors.Is(err, errNotFound) {
		err = put(db, "settings", "system", Settings{Timezone: "Asia/Shanghai", HistoryDays: 30})
	}
	if err != nil {
		cancel()
		return nil, err
	}
	if err = service.recoverRuns(); err != nil {
		cancel()
		return nil, err
	}
	service.wg.Add(1)
	go service.loop()
	return service, nil
}

func (service *Service) Close() error {
	service.cancel()
	service.wg.Wait()
	err := service.db.Close()
	if service.passwordFile != "" {
		_ = os.Remove(service.passwordFile)
	}
	if service.runtimeDir != "" {
		_ = os.Remove(service.runtimeDir)
	}
	unlockErr := service.lock.Unlock()
	if err != nil {
		return err
	}
	return unlockErr
}

func (service *Service) PasswordFile() string {
	if service.passwordSource != "generated" {
		return ""
	}
	return service.passwordFile
}

func (service *Service) digest(body []byte) string {
	mac := hmac.New(sha256.New, service.key)
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func (service *Service) encrypt(secret map[string]string) (string, error) {
	body, err := json.Marshal(secret)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, service.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(service.aead.Seal(nonce, nonce, body, []byte("cfst-credentials-v1"))), nil
}

func (service *Service) credentials(connection Connection) (integrations.Connection, error) {
	value := integrations.Connection{ID: connection.ID, Kind: connection.Kind, Name: connection.Name, AccountID: connection.AccountID}
	body, err := base64.StdEncoding.DecodeString(connection.Ciphertext)
	if err != nil || len(body) < service.aead.NonceSize() {
		return value, errors.New("服务连接密钥无法解密")
	}
	size := service.aead.NonceSize()
	plain, err := service.aead.Open(nil, body[:size], body[size:], []byte("cfst-credentials-v1"))
	if err != nil {
		return value, errors.New("服务连接密钥无法解密，请恢复匹配主密钥")
	}
	err = json.Unmarshal(plain, &value.Secrets)
	return value, err
}

func (service *Service) log(query querier, actor, action, object, result, reason string) error {
	entry := Log{ID: randomID(), At: service.now(), Actor: actor, Action: action, Object: object, Result: result, Reason: integrations.Redact(reason)}
	return put(query, "logs", entry.ID, entry)
}

func (service *Service) mutate(operationID, action string, request []byte, change func(*sql.Tx) (string, any, error)) (Operation, bool, error) {
	if len(operationID) < 8 || len(operationID) > 128 {
		return Operation{}, false, invalid("缺少有效 operation_id；请使用本次操作的固定标识")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.ctx.Err() != nil {
		return Operation{}, false, errors.New("服务正在停止，请稍后查询原操作收据")
	}
	requestHash := service.digest(append([]byte(action+"\x00"), request...))
	existing, err := get[Operation](service.db, "operations", operationID)
	if err == nil {
		if existing.RecoveryOperationID != "" || existing.State == "retired" {
			return Operation{}, false, &apiError{410, "operation_retired", "原操作已人工转为新尝试，不再受理原号；请查询关联核查收据"}
		}
		if existing.RequestHash != requestHash {
			return Operation{}, false, conflict("同一 operation_id 已用于不同请求，未执行本次请求")
		}
		if existing.State == "expired" {
			return Operation{}, false, &apiError{410, "operation_expired", "操作收据已过期，不能据此认定原操作未执行；请人工核对"}
		}
		return existing, false, nil
	}
	if !errors.Is(err, errNotFound) {
		return Operation{}, false, err
	}
	tx, err := service.db.Begin()
	if err != nil {
		return Operation{}, false, err
	}
	defer tx.Rollback()
	reference, err := service.consumeRecovery(tx, operationID, action, request)
	if err != nil {
		return Operation{}, false, err
	}
	id, result, err := change(tx)
	if err != nil {
		_ = tx.Rollback()
		if logErr := service.log(service.db, "共享管理身份", action, id, "rejected", safeError(err)); logErr != nil {
			return Operation{}, false, logErr
		}
		return Operation{}, false, err
	}
	state := "completed"
	fields := strings.Fields(action)
	if len(fields) >= 2 && fields[0] == "POST" {
		resource := fields[1]
		if strings.HasSuffix(resource, "/runs") || strings.Contains(resource, "/runs/") && (strings.HasSuffix(resource, "/cancel") || strings.HasSuffix(resource, "/dns-deliveries") || strings.HasSuffix(resource, "/notifications")) {
			state = "accepted"
		}
	}
	if state == "accepted" {
		if current, ok := result.(Run); ok && !active(current.Status) && !deliveryBusy(current) {
			state = "completed"
		}
	}
	result = compactOperationResult(result)
	operation := Operation{ID: operationID, ObjectID: id, State: state, Result: result, RequestHash: requestHash, CreatedAt: service.now(), Action: action, PriorOperationID: reference.PriorID, RecoveryID: reference.RecoveryID}
	if err = put(tx, "operations", operationID, operation); err == nil {
		err = service.log(tx, "共享管理身份", action, id, operation.State, "")
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return Operation{}, false, err
	}
	select {
	case service.wake <- struct{}{}:
	default:
	}
	return operation, true, nil
}

func targetKey(accountID, recordID string) string {
	digest := sha256.Sum256([]byte("alidns\x00" + accountID + "\x00" + recordID))
	return hex.EncodeToString(digest[:])
}

func (service *Service) priorOperation(operationID, action string, request []byte) (Operation, bool, error) {
	if len(operationID) < 8 || len(operationID) > 128 {
		return Operation{}, false, invalid("缺少有效 operation_id")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	operation, err := get[Operation](service.db, "operations", operationID)
	if errors.Is(err, errNotFound) {
		return Operation{}, false, nil
	}
	if err != nil {
		return Operation{}, false, err
	}
	if operation.RecoveryOperationID != "" || operation.State == "retired" {
		return Operation{}, false, &apiError{410, "operation_retired", "原操作已转人工处理，不再受理旧号"}
	}
	if operation.RequestHash != service.digest(append([]byte(action+"\x00"), request...)) {
		return Operation{}, false, conflict("同一 operation_id 已用于不同请求，未执行本次请求")
	}
	if operation.State == "expired" {
		return Operation{}, false, &apiError{410, "operation_expired", "收据已过期，请人工核对，不能将原号作为新操作重做"}
	}
	operation.RequestHash = ""
	return operation, true, nil
}

func (service *Service) targetLock(key string) *sync.Mutex {
	service.targetMu.Lock()
	defer service.targetMu.Unlock()
	if service.targetLocks[key] == nil {
		service.targetLocks[key] = new(sync.Mutex)
	}
	return service.targetLocks[key]
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	return integrations.Redact(fmt.Sprint(err))
}
