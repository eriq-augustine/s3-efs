package driver;

// The driver is responsible for handleing all the filesystem operations.
// A driver will have a connector that will handle the operations to the actual backend
// (eg local filesystem or S3).

import (
   "crypto/aes"
   "crypto/cipher"

   "github.com/pkg/errors"

   "github.com/eriq-augustine/elfs/cache"
   "github.com/eriq-augustine/elfs/connector"
   "github.com/eriq-augustine/elfs/dirent"
   "github.com/eriq-augustine/elfs/identity"
)

type Driver struct {
   connector connector.Connector
   blockCipher cipher.Block
   fatVersion int
   fat map[dirent.Id]*dirent.Dirent
   usersVersion int
   users map[identity.UserId]*identity.User
   groupsVersion int
   groups map[identity.GroupId]*identity.Group
   cache *cache.MetadataCache
   // A map of all directories to their children.
   dirs map[dirent.Id][]*dirent.Dirent
   // Base IV for metadata tables.
   iv []byte
   // Speific IVs for metadata tables.
   usersIV []byte
   groupsIV []byte
   fatIV []byte
   cacheIV []byte
}

// Get a new, uninitialized driver.
// Normally you will want to get a storage specific driver, like a NewLocalDriver.
// If you need a new filesystem, you should call CreateFilesystem().
// If you want to load up an existing filesystem, then you should call SyncFromDisk().
func newDriver(key []byte, iv []byte, connector connector.Connector) (*Driver, error) {
   blockCipher, err := aes.NewCipher(key)
   if err != nil {
      return nil, errors.WithStack(err);
   }

   var driver Driver = Driver{
      connector: connector,
      blockCipher: blockCipher,
      fatVersion: 0,
      fat: make(map[dirent.Id]*dirent.Dirent),
      usersVersion: 0,
      users: make(map[identity.UserId]*identity.User),
      groupsVersion: 0,
      groups: make(map[identity.GroupId]*identity.Group),
      cache: nil,
      dirs: make(map[dirent.Id][]*dirent.Dirent),
      iv: iv,
      usersIV: nil,
      groupsIV: nil,
      fatIV: nil,
      cacheIV: nil,
   };

   driver.initIVs();

   // Need to init the IVs before creating the cache.
   cache, err := cache.NewMetadataCache(connector, blockCipher, driver.cacheIV);
   if (err != nil) {
      return nil, errors.WithStack(err);
   }
   driver.cache = cache;

   return &driver, nil;
}
