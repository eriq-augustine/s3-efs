package main;

import (
   "bufio"
   "encoding/hex"
   "flag"
   "fmt"
   "io"
   "io/ioutil"
   "os"
   "path/filepath"
   "strconv"
   "strings"

   "github.com/pkg/errors"
   shellquote "github.com/kballard/go-shellquote"

   "github.com/eriq-augustine/elfs/cipherio"
   "github.com/eriq-augustine/elfs/connector"
   "github.com/eriq-augustine/elfs/dirent"
   "github.com/eriq-augustine/elfs/driver"
   "github.com/eriq-augustine/elfs/group"
   "github.com/eriq-augustine/elfs/user"
   "github.com/eriq-augustine/elfs/util"
)

// Params: (invocation name, fs driver, args (not including invocation)).
type commandFunction func(string, *driver.Driver, []string) error;

const (
   COMMAND_CREATE = "create"
   COMMAND_LOGIN = "login"
   COMMAND_QUIT = "quit"
   AWS_CRED_PATH = "config/elfs-aws-credentials"
   AWS_PROFILE = "elfsapi"
   AWS_REGION = "us-west-2"
)

var commands map[string]commandFunction;
var activeUser *user.User;

func init() {
   activeUser = nil;

   commands = make(map[string]commandFunction);

   commands["cat"] = cat;
   commands[COMMAND_CREATE] = create;
   commands["demote"] = demote;
   commands["export"] = export;
   commands["groupadd"] = groupadd;
   commands["groupdel"] = groupdel;
   commands["groupjoin"] = groupjoin;
   commands["groupkick"] = groupkick;
   commands["grouplist"] = grouplist;
   commands["help"] = help;
   commands["import"] = importFile;
   commands[COMMAND_LOGIN] = login;
   commands["ls"] = ls;
   commands["mkdir"] = mkdir;
   commands["mv"] = move;
   commands["promote"] = promote;
   commands["rename"] = rename;
   commands["rm"] = remove;
   commands["useradd"] = useradd;
   commands["userdel"] = userdel;
   commands["userlist"] = userlist;
   commands["chown"] = chown;
   commands["permadd"] = permissionAdd;
   commands["permdel"] = permissionDelete;
}

func main() {
   key, iv, connectorType, path, err := parseArgs();
   if (err != nil) {
      flag.Usage();
      fmt.Printf("Error parsing args: %+v\n", err);
      return;
   }

   var fsDriver *driver.Driver = nil;
   if (connectorType == connector.CONNECTOR_TYPE_LOCAL) {
      fsDriver, err = driver.NewLocalDriver(key, iv, path);
      if (err != nil) {
         panic(fmt.Sprintf("%+v", errors.Wrap(err, "Failed to get local driver")));
      }
   } else if (connectorType == connector.CONNECTOR_TYPE_S3) {
      fsDriver, err = driver.NewS3Driver(key, iv, path, AWS_CRED_PATH, AWS_PROFILE, AWS_REGION);
      if (err != nil) {
         panic(fmt.Sprintf("%+v", errors.Wrap(err, "Failed to get S3 driver")));
      }
   } else {
      panic(fmt.Sprintf("Unknown connector type: [%s]", connectorType));
   }

   var scanner *bufio.Scanner = bufio.NewScanner(os.Stdin);
   for {
      if (activeUser == nil) {
         fmt.Printf("> ");
      } else {
         fmt.Printf("%s > ", activeUser.Name);
      }

      if (!scanner.Scan()) {
         break;
      }

      var command string = strings.TrimSpace(scanner.Text());

      if (command == "") {
         continue;
      }

      if (strings.HasPrefix(command, COMMAND_QUIT)) {
         break;
      }

      err = processCommand(fsDriver, command);
      if (err != nil) {
         fmt.Println("Failed to run command:");
         fmt.Printf("%+v\n", err);
      }
   }
   fmt.Println("");

   fsDriver.Close();
}

// Returns: (key, iv, connector type, path).
func parseArgs() ([]byte, []byte, string, string, error) {
   var hexKey *string = flag.String("key", "", "the encryption key in hex");
   var hexIV *string = flag.String("iv", "", "the IV in hex");
   var connectorType *string = flag.String("type", "", "the connector type ('S3' or 'local')");
   var path *string = flag.String("path", "", "the path to the filesystem");
   flag.Parse();

   if (hexKey == nil || *hexKey == "") {
      return nil, nil, "", "", errors.New("Error: Key required.");
   }

   if (hexIV == nil || *hexIV == "") {
      return nil, nil, "", "", errors.New("Error: IV required.");
   }

   if (connectorType == nil || *connectorType == "") {
      // Can't take the address of a constant.
      var tempType string = connector.CONNECTOR_TYPE_LOCAL;
      connectorType = &tempType;
   }

   if (path == nil || *path == "") {
      return nil, nil, "", "", errors.New("Error: Path required.");
   }

   key, err := hex.DecodeString(*hexKey);
   if (err != nil) {
      return nil, nil, "", "", errors.Wrap(err, "Could not decode hex key.");
   }

   iv, err := hex.DecodeString(*hexIV);
   if (err != nil) {
      return nil, nil, "", "", errors.Wrap(err, "Could not decode hex iv.");
   }

   return key, iv, *connectorType, *path, nil;
}

func processCommand(fsDriver *driver.Driver, command string) error {
   args, err := shellquote.Split(command);
   if (err != nil) {
      return errors.Wrap(err, "Failed to split command.");
   }

   var operation string = args[0];
   args = args[1:];

   // Only allow login and create commands if no one is logged in.
   if (activeUser == nil && operation != COMMAND_LOGIN && operation != COMMAND_CREATE) {
      return errors.New("Need to login.");
   }

   commandFunc, ok := commands[operation];
   if (!ok) {
      return errors.New("Unknown operation: " + operation);
   }

   return errors.Wrap(commandFunc(operation, fsDriver, args), "Failed to run command");
};

func cat(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) < 1) {
      return errors.New(fmt.Sprintf("USAGE: %s <file> ...", command));
   }

   var buffer []byte = make([]byte, cipherio.IO_BLOCK_SIZE);

   for _, arg := range(args) {
      // Reset the buffer from the last read.
      buffer = buffer[0:cap(buffer)];

      reader, err := fsDriver.Read(activeUser.Id, dirent.Id(arg));
      if (err != nil) {
         return errors.Wrap(err, "Failed to open fs file for reading: " + arg);
      }

      var done bool = false;
      for (!done) {
         readSize, err := reader.Read(buffer);
         if (err != nil) {
            if (err != io.EOF) {
               return errors.Wrap(err, "Failed to read fs file: " + arg);
            }

            done = true;
         }

         if (readSize > 0) {
            fmt.Print(string(buffer[0:readSize]));
         }
      }

      fmt.Println("");
      reader.Close();
   }

   return nil;
}

func export(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) != 2) {
      return errors.New(fmt.Sprintf("USAGE: %s <file> <external path>", command));
   }

   var source dirent.Id = dirent.Id(args[0]);
   var dest string = args[1];

   fileInfo, err := fsDriver.GetDirent(activeUser.Id, source);
   if (err != nil) {
      return errors.Wrap(err, "Failed to get dirent for export");
   }

   if (!fileInfo.IsFile) {
      return errors.New("Recursive export is currently not supported.");
   }

   // Check if the external path is a directory.
   // If so, make the target path that directory with the file's current name.
   stat, err := os.Stat(dest);
   if (err == nil && stat.IsDir()) {
      dest = filepath.Join(dest, fileInfo.Name);
   }

   outFile, err := os.Create(dest);
   if (err != nil) {
      return errors.Wrap(err, "Failed to create outout file for export.");
   }
   defer outFile.Close();

   var buffer []byte = make([]byte, cipherio.IO_BLOCK_SIZE);

   reader, err := fsDriver.Read(activeUser.Id, source);
   if (err != nil) {
      return errors.Wrap(err, "Failed to open fs file for reading: " + string(source));
   }
   defer reader.Close();

   var done bool = false;
   for (!done) {
      readSize, err := reader.Read(buffer);
      if (err != nil) {
         if (err != io.EOF) {
            return errors.Wrap(err, "Failed to read fs file: " + string(source));
         }

         done = true;
      }

      if (readSize > 0) {
         outFile.Write(buffer[0:readSize]);
      }
   }

   return nil;
}

func create(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) != 1) {
      return errors.New(fmt.Sprintf("USAGE: %s <root password>", command));
   }

   return fsDriver.CreateFilesystem(util.ShaHash(args[0]));
}

func help(command string, fsDriver *driver.Driver, args []string) error {
   return errors.New("Operation not implemented.");
}

func importFile(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) < 1 || len(args) > 2) {
      return errors.New(fmt.Sprintf("USAGE: %s <external file> [parent id]", command));
   }

   var localPath string = args[0];

   var parent dirent.Id = dirent.ROOT_ID;
   if (len(args) == 2) {
      parent = dirent.Id(args[1]);
   }

   return errors.WithStack(recursiveImport(fsDriver, localPath, parent));
}

func importFileInternal(fsDriver *driver.Driver, path string, parent dirent.Id) error {
   fileReader, err := os.Open(path);
   if (err != nil) {
      return errors.Wrap(err, path);
   }
   defer fileReader.Close();

   err = fsDriver.Put(activeUser.Id, filepath.Base(path), fileReader, map[group.Id]group.Permission{}, parent);
   if (err != nil) {
      return errors.Wrap(err, path);
   }

   return nil;
}

func recursiveImport(fsDriver *driver.Driver, path string, parent dirent.Id) error {
   fileInfo, err := os.Stat(path);
   if (err != nil) {
      return errors.Wrap(err, path);
   }

   if (!fileInfo.IsDir()) {
      return errors.WithStack(importFileInternal(fsDriver, path, parent));
   }

   // First make the actual dir and then import the children.
   newId, err := fsDriver.MakeDir(activeUser.Id, fileInfo.Name(), parent, map[group.Id]group.Permission{});
   if (err != nil) {
      return errors.Wrap(err, path);
   }

   children, err := ioutil.ReadDir(path);
   if (err != nil) {
      return errors.Wrap(err, path);
   }

   for _, child := range(children) {
      err = recursiveImport(fsDriver, filepath.Join(path, child.Name()), newId);
      if (err != nil) {
         return errors.Wrap(err, path);
      }
   }

   return nil;
}

func login(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) != 2) {
      return errors.New(fmt.Sprintf("USAGE: %s <username> <password>", command));
   }

   authUser, err := fsDriver.UserAuth(args[0], util.ShaHash(args[1]));
   if (err != nil) {
      return errors.Wrap(err, "Failed to authenticate user.");
   }

   activeUser = authUser;
   return nil;
}

func ls(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) > 1) {
      return errors.New(fmt.Sprintf("USAGE: %s [dir id]", command));
   }

   var id dirent.Id = dirent.ROOT_ID;
   if (len(args) == 1) {
      id = dirent.Id(args[0]);
   }

   entries, err := fsDriver.List(activeUser.Id, id);
   if (err != nil) {
      return errors.Wrap(err, "Failed to list directory: " + string(id));
   }

   var parts []string = make([]string, 0);
   var groups []string = make([]string, 0);

   for _, entry := range(entries) {
      parts = parts[:0];
      groups = parts[:0];

      var direntType string = "D";
      if (entry.IsFile) {
         direntType = "F";
      }

      parts = append(parts, entry.Name, direntType,
            string(entry.Id), fmt.Sprintf("%d", entry.Size), fmt.Sprintf("%d", entry.ModTimestamp), entry.Md5);

      // Get the group permissions.
      for groupId, permission := range(entry.GroupPermissions) {
         var access string = "";

         if (permission.Read) {
            access += "R";
         } else {
            access += "-";
         }

         if (permission.Write) {
            access += "W";
         } else {
            access += "-";
         }

         groups = append(groups, fmt.Sprintf("%s: %s", groupId, access));
      }
      parts = append(parts, fmt.Sprintf("[%s]", strings.Join(groups, ", ")));

      fmt.Println(strings.Join(parts, "\t"));
   }

   return nil;
}

func mkdir(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) < 1 || len(args) > 2) {
      return errors.New(fmt.Sprintf("USAGE: %s <dir name> [parent id]", command));
   }

   var name string = args[0];

   var parent dirent.Id = dirent.ROOT_ID;
   if (len(args) == 2) {
      parent = dirent.Id(args[1]);
   }

   id, err := fsDriver.MakeDir(activeUser.Id, name, parent, map[group.Id]group.Permission{});
   if (err != nil) {
      return errors.Wrap(err, "Failed to make dir: " + name);
   }

   fmt.Println(id);

   return nil;
}

func move(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) != 2) {
      return errors.New(fmt.Sprintf("USAGE: %s <target id> <new parent id>", command));
   }

   var targetId dirent.Id = dirent.Id(args[0]);
   var newParentId dirent.Id = dirent.Id(args[1]);

   return errors.WithStack(fsDriver.Move(activeUser.Id, targetId, newParentId));
}

func rename(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) != 2) {
      return errors.New(fmt.Sprintf("USAGE: %s <target id> <new name>", command));
   }

   var targetId dirent.Id = dirent.Id(args[0]);

   return errors.WithStack(fsDriver.Rename(activeUser.Id, targetId, args[1]));
}

func remove(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) < 1 || len(args) > 2 || (len(args) == 2 && args[0] != "-r")) {
      return errors.New(fmt.Sprintf("USAGE: %s [-r] <dirent id>", command));
   }

   var isFile = true;
   if (len(args) == 2) {
      isFile = false;
      args = args[1:];
   }

   var direntId dirent.Id = dirent.Id(args[0]);

   var err error = nil;
   if (isFile) {
      err = fsDriver.RemoveFile(activeUser.Id, direntId);
   } else {
      err = fsDriver.RemoveDir(activeUser.Id, direntId);
   }

   return errors.WithStack(err);
}

func useradd(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) != 2) {
      return errors.New(fmt.Sprintf("USAGE: %s <username> <password>", command));
   }

   _, err := fsDriver.AddUser(activeUser.Id, args[0], util.ShaHash(args[1]));
   return errors.Wrap(err, "Failed to add user");
}

func userdel(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) != 1) {
      return errors.New(fmt.Sprintf("USAGE: %s <username>", command));
   }

   userId, err := strconv.Atoi(args[0]);
   if (err != nil) {
      return errors.Wrap(err, "Failed to parse user id");
   }

   err = fsDriver.RemoveUser(activeUser.Id, user.Id(userId));
   return errors.Wrap(err, "Failed to remove user");
}

func userlist(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) != 0) {
      return errors.New(fmt.Sprintf("USAGE: %s", command));
   }

   users := fsDriver.GetUsers();

   for _, user := range(users) {
      fmt.Printf("%s\t%d\n", user.Name, int(user.Id));
   }

   return nil;
}

func demote(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) != 2) {
      return errors.New(fmt.Sprintf("USAGE: %s <group id> <user id>", command));
   }

   groupId, err := strconv.Atoi(args[0]);
   if (err != nil) {
      return errors.Wrap(err, args[0]);
   }

   userId, err := strconv.Atoi(args[1]);
   if (err != nil) {
      return errors.Wrap(err, args[1]);
   }

   return errors.WithStack(fsDriver.DemoteUser(activeUser.Id, user.Id(userId), group.Id(groupId)));
}

func groupadd(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) != 1) {
      return errors.New(fmt.Sprintf("USAGE: %s <group name>", command));
   }

   newId, err := fsDriver.AddGroup(activeUser.Id, args[0]);
   if (err != nil) {
      return errors.WithStack(err);
   }

   fmt.Println(newId);
   return nil;
}

func groupdel(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) != 1) {
      return errors.New(fmt.Sprintf("USAGE: %s <group id>", command));
   }

   groupId, err := strconv.Atoi(args[0]);
   if (err != nil) {
      return errors.Wrap(err, args[0]);
   }

   return errors.WithStack(fsDriver.DeleteGroup(activeUser.Id, group.Id(groupId)));
}

func groupjoin(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) != 2) {
      return errors.New(fmt.Sprintf("USAGE: %s <group id> <user id>", command));
   }

   groupId, err := strconv.Atoi(args[0]);
   if (err != nil) {
      return errors.Wrap(err, args[0]);
   }

   userId, err := strconv.Atoi(args[1]);
   if (err != nil) {
      return errors.Wrap(err, args[1]);
   }

   return errors.WithStack(fsDriver.JoinGroup(activeUser.Id, user.Id(userId), group.Id(groupId)));
}

func groupkick(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) != 2) {
      return errors.New(fmt.Sprintf("USAGE: %s <group id> <user id>", command));
   }

   groupId, err := strconv.Atoi(args[0]);
   if (err != nil) {
      return errors.Wrap(err, args[0]);
   }

   userId, err := strconv.Atoi(args[1]);
   if (err != nil) {
      return errors.Wrap(err, args[1]);
   }

   return errors.WithStack(fsDriver.KickUser(activeUser.Id, user.Id(userId), group.Id(groupId)));
}

func grouplist(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) != 0) {
      return errors.New(fmt.Sprintf("USAGE: %s", command));
   }

   groups := fsDriver.GetGroups();

   var parts []string = make([]string, 0);
   for _, group := range(groups) {
      parts = parts[:0];

      parts = append(parts, group.Name);
      parts = append(parts, fmt.Sprintf("%d", int(group.Id)));

      for userId, _ := range(group.Users) {
         var name string;
         if (group.Admins[userId]) {
            name = fmt.Sprintf("%d*", int(userId));
         } else {
            name = fmt.Sprintf("%d", int(userId));
         }

         parts = append(parts, name);
      }

      fmt.Println(strings.Join(parts, "\t"));
   }

   return nil;
}

func promote(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) != 2) {
      return errors.New(fmt.Sprintf("USAGE: %s <group id> <user id>", command));
   }

   groupId, err := strconv.Atoi(args[0]);
   if (err != nil) {
      return errors.Wrap(err, args[0]);
   }

   userId, err := strconv.Atoi(args[1]);
   if (err != nil) {
      return errors.Wrap(err, args[1]);
   }

   return errors.WithStack(fsDriver.PromoteUser(activeUser.Id, user.Id(userId), group.Id(groupId)));
}

func chown(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) != 2) {
      return errors.New(fmt.Sprintf("USAGE: %s <dirent id> <new owner id>", command));
   }

   var direntId dirent.Id = dirent.Id(args[0]);

   userId, err := strconv.Atoi(args[1]);
   if (err != nil) {
      return errors.Wrap(err, args[1]);
   }

   return errors.WithStack(fsDriver.ChangeOwner(activeUser.Id, direntId, user.Id(userId)));
}

func permissionAdd(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) != 3) {
      return errors.New(fmt.Sprintf("USAGE: %s <dirent id> <group id> <2|4|6>", command));
   }

   var direntId dirent.Id = dirent.Id(args[0]);

   groupId, err := strconv.Atoi(args[1]);
   if (err != nil) {
      return errors.Wrap(err, args[1]);
   }

   permission, err := strconv.Atoi(args[2]);
   if (err != nil) {
      return errors.Wrap(err, args[2]);
   }

   if (permission != 2 && permission != 4 && permission != 6) {
      return errors.Errorf("Bad permission number: %d. Use UNIX-style for read and write", permission);
   }

   var read bool = (permission % 4 == 0);
   var write bool = (permission % 2 == 0);

   return errors.WithStack(fsDriver.PutGroupAccess(activeUser.Id, direntId, group.Id(groupId), group.NewPermission(read, write)));
}

func permissionDelete(command string, fsDriver *driver.Driver, args []string) error {
   if (len(args) != 2) {
      return errors.New(fmt.Sprintf("USAGE: %s <dirent id> <group id>", command));
   }

   var direntId dirent.Id = dirent.Id(args[0]);

   groupId, err := strconv.Atoi(args[1]);
   if (err != nil) {
      return errors.Wrap(err, args[1]);
   }

   return errors.WithStack(fsDriver.RemoveGroupAccess(activeUser.Id, direntId, group.Id(groupId)));
}
