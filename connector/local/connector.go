package local;

// A connector that just works on a local disk.
// This treats a directory as if it was a partition.

import (
    "crypto/cipher"
    "fmt"
    "io/ioutil"
    "os"
    "path/filepath"
    "sync"

    "github.com/pkg/errors"

    "github.com/eriq-augustine/elfs/cipherio"
    "github.com/eriq-augustine/elfs/connector"
    "github.com/eriq-augustine/elfs/dirent"
    "github.com/eriq-augustine/elfs/util"
)

// Keep track of the active connections so two instances don't connect to the same storage.
var activeConnections map[string]bool;
var activeConnectionsLock *sync.Mutex;

func init() {
    activeConnections = make(map[string]bool);
    activeConnectionsLock = &sync.Mutex{};
}

type LocalConnector struct {
    path string
}

// Create a new connection to a local filesystem.
// There should only ever be one connection to a filesystem at a time.
// If an old connection has not been properly closed, then the force parameter
// may be used to cleanup the old connection.
func NewLocalConnector(path string, force bool) (*LocalConnector, error) {
    activeConnectionsLock.Lock();
    defer activeConnectionsLock.Unlock();

    path, err := filepath.Abs(path);
    if (err != nil) {
        return nil, errors.Wrap(err, "Failed to create absolute path for local connector.");
    }

    _, ok := activeConnections[path];
    if (ok) {
        return nil, errors.Errorf("Cannot create two connections to the same storage: %s", path);
    }

    os.MkdirAll(filepath.Join(path, connector.FS_SYS_DIR_ADMIN), 0700);

    var connector LocalConnector = LocalConnector {
        path: path,
    };

    err = connector.lock(force);
    if (err != nil) {
        return nil, errors.Wrap(err, path);
    }

    return &connector, nil;
}

func (this *LocalConnector) GetId() string {
    return connector.CONNECTOR_TYPE_LOCAL + ":" + this.path;
}

func (this *LocalConnector) PrepareStorage() error {
    var dirs []string = make([]string, 0, 3 + len(util.RANDOM_CHARS));

    dirs = append(dirs,
        this.path,
        filepath.Join(this.path, connector.FS_SYS_DIR_ADMIN),
        filepath.Join(this.path, connector.FS_SYS_DIR_DATA),
    );

    // HACK(eriq): Instead of bothering with potential crossproduct, just deal with the single length prefix.
    if (connector.DATA_GROUP_PREFIX_LEN != 1) {
        return errors.Errorf("Expected data prefix to be 1, not %d.", connector.DATA_GROUP_PREFIX_LEN);
    }

    for _, prefix := range(util.RANDOM_CHARS) {
        dirs = append(dirs, filepath.Join(this.path, connector.FS_SYS_DIR_DATA, string(prefix)));
    }

    for _, dir := range(dirs) {
        err := os.MkdirAll(dir, 0700);
        if (err != nil) {
            return errors.WithStack(err);
        }
    }

    return nil;
}

func (this *LocalConnector) GetCipherReader(fileInfo *dirent.Dirent, blockCipher cipher.Block) (util.ReadSeekCloser, error) {
    var path string = this.getDiskPath(fileInfo);

    file, err := os.Open(path);
    if (err != nil) {
        return nil, errors.Wrap(err, "Unable to open file on disk at: " + path);
    }

    fileStat, err := file.Stat();
    if (err != nil) {
        return nil, errors.WithStack(err);
    }

    return cipherio.NewCipherReader(file, blockCipher, fileInfo.IV, fileStat.Size());
}

func (this *LocalConnector) GetMetadataReader(metadataId string, blockCipher cipher.Block, iv []byte) (util.ReadSeekCloser, error) {
    var path string = this.getMetadataPath(metadataId);

    file, err := os.Open(path);
    if (err != nil) {
        return nil, errors.Wrap(err, "Unable to open file on disk at: " + path);
    }

    fileStat, err := file.Stat();
    if (err != nil) {
        return nil, errors.WithStack(err);
    }

    return cipherio.NewCipherReader(file, blockCipher, iv, fileStat.Size());
}

func (this *LocalConnector) GetCipherWriter(fileInfo *dirent.Dirent, blockCipher cipher.Block) (*cipherio.CipherWriter, error) {
    var path string = this.getDiskPath(fileInfo);

    file, err := os.Create(path);
    if (err != nil) {
        return nil, errors.Wrap(err, "Unable to create file on disk at: " + path);
    }

    err = file.Chmod(0600);
    if (err != nil) {
        return nil, errors.Wrap(err, "Unable to change file permissions of: " + path);
    }

    return cipherio.NewCipherWriter(file, blockCipher, fileInfo.IV);
}

func (this *LocalConnector) GetMetadataWriter(metadataId string, blockCipher cipher.Block, iv []byte) (*cipherio.CipherWriter, error) {
    var path string = this.getMetadataPath(metadataId);

    file, err := os.Create(path);
    if (err != nil) {
        return nil, errors.Wrap(err, "Unable to create file on disk at: " + path);
    }

    err = file.Chmod(0600);
    if (err != nil) {
        return nil, errors.Wrap(err, "Unable to change file permissions of: " + path);
    }

    return cipherio.NewCipherWriter(file, blockCipher, iv);
}

func (this *LocalConnector) RemoveFile(file *dirent.Dirent) error {
    return errors.WithStack(os.Remove(this.getDiskPath(file)));
}

func (this *LocalConnector) RemoveMetadataFile(metadataId string) error {
    return errors.WithStack(os.Remove(this.getMetadataPath(metadataId)));
}

func (this* LocalConnector) Close() error {
    activeConnectionsLock.Lock();
    defer activeConnectionsLock.Unlock();

    activeConnections[this.path] = false;
    return errors.WithStack(this.unlock());
}

func (this* LocalConnector) lock(force bool) error {
    var lockPath string = this.getLockPath();

    inFile, err := os.Open(lockPath);
    if (err != nil && !os.IsNotExist(err)) {
        return errors.Wrap(err, lockPath);
    }
    defer inFile.Close();

    // Lock already exists and we were not told to force it.
    if (err == nil && !force) {
        pid, err := ioutil.ReadAll(inFile);
        if (err != nil) {
            return errors.Wrap(err, lockPath);
        }

        return errors.Errorf("Local filesystem (at %s) already owned by [%s]." +
                " Ensure that the processes is dead and remove the lock or force the connector.",
                this.path, string(pid));
    }

    // Lock doesn't exist, or we can force it.
    return errors.Wrap(ioutil.WriteFile(lockPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0600), lockPath);
}

func (this* LocalConnector) unlock() error {
    var lockPath string = this.getLockPath();
    return errors.Wrap(os.Remove(lockPath), lockPath);
}
