package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

const version = "0.1.0"

type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ", ")
}

func (s *stringSliceFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}

type CLIOptions struct {
	Shipitfile   string
	RequireFiles []string
	ShowTasks    bool
	ShowEnvs     bool
	ShowVersion  bool
	ShowHelp     bool
	Environment  string
	Tasks        []string
}

type Config struct {
	Environments map[string]Environment `yaml:"environments"`
	Default      DefaultConfig          `yaml:"default"`
}

type Environment struct {
	Servers  []string `yaml:"servers"`
	DeployTo string   `yaml:"deployTo"`
	Branch   string   `yaml:"branch"`
}

type DefaultConfig struct {
	Workspace        string            `yaml:"workspace"`
	RepositoryURL    string            `yaml:"repositoryUrl"`
	Ignores          []string          `yaml:"ignores"`
	KeepReleases     int               `yaml:"keepReleases"`
	DeleteOnRollback bool              `yaml:"deleteOnRollback"`
	DirToCopy        string            `yaml:"dirToCopy"`
	Tasks            map[string]string `yaml:"tasks"`
	Published        map[string]string `yaml:"published"`
}

type Runner struct {
	cfg Config
	env string
}

func main() {
	options, err := parseCLI(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		printUsage(os.Stderr)
		os.Exit(2)
	}

	if options.ShowHelp {
		printUsage(os.Stdout)
		return
	}
	if options.ShowVersion {
		fmt.Println(version)
		return
	}

	cfg, err := loadConfig(options.Shipitfile, options.RequireFiles)
	if err != nil {
		log.Fatal(err)
	}

	if options.ShowEnvs {
		for _, name := range environmentNames(cfg) {
			fmt.Println(name)
		}
		return
	}

	if options.ShowTasks {
		for _, task := range taskNames(cfg) {
			fmt.Println(task)
		}
		return
	}

	if options.Environment == "" {
		printUsage(os.Stderr)
		os.Exit(2)
	}

	runner := Runner{cfg: cfg, env: options.Environment}
	runner.logf("environment=%s tasks=%s shipitfile=%s", options.Environment, strings.Join(options.Tasks, ","), options.Shipitfile)
	for _, task := range options.Tasks {
		if err := runner.Run(task); err != nil {
			log.Fatal(err)
		}
	}
}

func parseCLI(args []string) (CLIOptions, error) {
	var options CLIOptions
	var requireFiles stringSliceFlag

	fs := flag.NewFlagSet("shipit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&options.Shipitfile, "shipitfile", "shipit.yaml", "Specify a custom shipitfile to use")
	fs.Var(&requireFiles, "require", "Script required before launching Shipit")
	fs.BoolVar(&options.ShowTasks, "tasks", false, "List available tasks")
	fs.BoolVar(&options.ShowEnvs, "environments", false, "List available environments")
	fs.BoolVar(&options.ShowVersion, "version", false, "output the version number")
	fs.BoolVar(&options.ShowVersion, "V", false, "output the version number")
	fs.BoolVar(&options.ShowHelp, "help", false, "output usage information")
	fs.BoolVar(&options.ShowHelp, "h", false, "output usage information")

	if err := fs.Parse(args); err != nil {
		return options, err
	}

	options.RequireFiles = requireFiles

	rest := fs.Args()
	if len(rest) > 0 {
		options.Environment = rest[0]
	}
	if len(rest) > 1 {
		options.Tasks = rest[1:]
	}

	if options.Environment != "" && len(options.Tasks) == 0 && !options.ShowTasks && !options.ShowEnvs {
		options.Tasks = []string{"deploy"}
	}

	return options, nil
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: shipit <environment> <tasks...>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Options:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  -V, --version         output the version number")
	fmt.Fprintln(w, "  --shipitfile <file>   Specify a custom shipitfile to use")
	fmt.Fprintln(w, "  --require <files...>  Script required before launching Shipit")
	fmt.Fprintln(w, "  --tasks               List available tasks")
	fmt.Fprintln(w, "  --environments        List available environments")
	fmt.Fprintln(w, "  -h, --help            output usage information")
}

func loadConfig(path string, requireFiles []string) (Config, error) {
	cfg, err := readConfigFile(path)
	if err != nil {
		return Config{}, err
	}

	for _, requireFile := range requireFiles {
		if _, err := os.Stat(requireFile); err != nil {
			return Config{}, fmt.Errorf("read require file %q: %w", requireFile, err)
		}

		ext := strings.ToLower(filepath.Ext(requireFile))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		overlay, err := readConfigFile(requireFile)
		if err != nil {
			return Config{}, fmt.Errorf("load require file %q: %w", requireFile, err)
		}
		mergeConfig(&cfg, overlay)
	}

	if cfg.Default.KeepReleases <= 0 {
		cfg.Default.KeepReleases = 5
	}

	return cfg, nil
}

func readConfigFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}

func mergeConfig(dst *Config, src Config) {
	if dst.Environments == nil {
		dst.Environments = map[string]Environment{}
	}
	for name, env := range src.Environments {
		dst.Environments[name] = env
	}

	if src.Default.Workspace != "" {
		dst.Default.Workspace = src.Default.Workspace
	}
	if src.Default.RepositoryURL != "" {
		dst.Default.RepositoryURL = src.Default.RepositoryURL
	}
	if len(src.Default.Ignores) > 0 {
		dst.Default.Ignores = append([]string(nil), src.Default.Ignores...)
	}
	if src.Default.KeepReleases > 0 {
		dst.Default.KeepReleases = src.Default.KeepReleases
	}
	if src.Default.DeleteOnRollback {
		dst.Default.DeleteOnRollback = true
	}
	if src.Default.DirToCopy != "" {
		dst.Default.DirToCopy = src.Default.DirToCopy
	}
	if dst.Default.Tasks == nil {
		dst.Default.Tasks = map[string]string{}
	}
	for name, task := range src.Default.Tasks {
		dst.Default.Tasks[name] = task
	}
	if dst.Default.Published == nil {
		dst.Default.Published = map[string]string{}
	}
	for name, hook := range src.Default.Published {
		dst.Default.Published[name] = hook
	}
}

func environmentNames(cfg Config) []string {
	names := make([]string, 0, len(cfg.Environments))
	for name := range cfg.Environments {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func taskNames(cfg Config) []string {
	taskSet := map[string]struct{}{
		"deploy":   {},
		"rollback": {},
	}
	for name := range cfg.Default.Tasks {
		taskSet[name] = struct{}{}
	}

	names := make([]string, 0, len(taskSet))
	for name := range taskSet {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r Runner) Run(task string) error {
	r.logf("start task=%s", task)
	switch task {
	case "deploy":
		err := r.deploy()
		if err == nil {
			r.logf("finish task=%s", task)
		}
		return err
	case "rollback":
		err := r.rollback()
		if err == nil {
			r.logf("finish task=%s", task)
		}
		return err
	default:
		err := r.runRemoteTask(task)
		if err == nil {
			r.logf("finish task=%s", task)
		}
		return err
	}
}

func (r Runner) deploy() error {
	env, err := r.environment()
	if err != nil {
		return err
	}

	workspace, releaseName, sourceDir, err := r.prepareWorkspace(env)
	if err != nil {
		return err
	}

	releasePath := filepath.ToSlash(filepath.Join(env.DeployTo, "releases", releaseName))
	r.logf("deploy release=%s workspace=%s source=%s", releasePath, workspace, sourceDir)

	if err := r.ensureRemoteDirs(env, releasePath); err != nil {
		return err
	}

	if err := r.copyRelease(env, sourceDir, releasePath); err != nil {
		return err
	}

	if err := r.updateCurrentSymlink(env, releasePath); err != nil {
		return err
	}

	if err := r.cleanupReleases(env); err != nil {
		return err
	}

	if err := r.runPublishedHook(); err != nil {
		return err
	}

	r.logf("cleanup local workspace=%s", workspace)
	return os.RemoveAll(workspace)
}

func (r Runner) rollback() error {
	env, err := r.environment()
	if err != nil {
		return err
	}

	releasesDir := filepath.ToSlash(filepath.Join(env.DeployTo, "releases"))
	currentLink := filepath.ToSlash(filepath.Join(env.DeployTo, "current"))

	for _, server := range env.Servers {
		r.logf("rollback inspect server=%s releases=%s", server, releasesDir)
		releases, err := listRemoteDirs(server, releasesDir)
		if err != nil {
			return err
		}
		if len(releases) < 2 {
			return fmt.Errorf("rollback requires at least 2 releases on %s", server)
		}

		sort.Strings(releases)
		currentTarget, err := readRemoteSymlink(server, currentLink)
		if err != nil {
			return err
		}
		currentRelease := filepath.Base(strings.TrimSpace(currentTarget))
		index := indexOf(releases, currentRelease)
		if index == -1 {
			return fmt.Errorf("current release %q not found under %s on %s", currentRelease, releasesDir, server)
		}
		if index == 0 {
			return fmt.Errorf("current release %q is already the oldest release on %s", currentRelease, server)
		}

		previousRelease := releases[index-1]
		previousPath := filepath.ToSlash(filepath.Join(releasesDir, previousRelease))
		currentPath := filepath.ToSlash(filepath.Join(releasesDir, currentRelease))

		r.logf("rollback switch server=%s from=%s to=%s", server, currentRelease, previousRelease)
		command := fmt.Sprintf("ln -sfn %s %s", shellQuote(previousPath), shellQuote(currentLink))
		if err := runSSHCommand(server, command, os.Stdout, os.Stderr); err != nil {
			return err
		}

		if r.cfg.Default.DeleteOnRollback {
			r.logf("rollback delete failed release server=%s release=%s", server, currentRelease)
			command := "rm -rf " + shellQuote(currentPath)
			if err := runSSHCommand(server, command, os.Stdout, os.Stderr); err != nil {
				return err
			}
		}
	}

	return r.runPublishedHook()
}

func (r Runner) runRemoteTask(task string) error {
	command, ok := r.cfg.Default.Tasks[task]
	if !ok {
		return fmt.Errorf("unsupported task %q", task)
	}

	env, err := r.environment()
	if err != nil {
		return err
	}

	r.logf("remote task=%s command=%s", task, command)
	for _, server := range env.Servers {
		r.logf("run task=%s server=%s", task, server)
		if err := runSSHCommand(server, command, os.Stdout, os.Stderr); err != nil {
			return err
		}
	}

	return nil
}

func (r Runner) runPublishedHook() error {
	command, ok := r.cfg.Default.Published[r.env]
	if !ok || strings.TrimSpace(command) == "" {
		return nil
	}

	env, err := r.environment()
	if err != nil {
		return err
	}

	r.logf("run published hook environment=%s", r.env)
	for _, server := range env.Servers {
		r.logf("run published server=%s", server)
		if err := runSSHCommand(server, command, os.Stdout, os.Stderr); err != nil {
			return err
		}
	}

	return nil
}

func (r Runner) prepareWorkspace(env Environment) (string, string, string, error) {
	releaseName := time.Now().UTC().Format("20060102150405")
	workspace := filepath.Join(r.cfg.Default.Workspace, r.env, releaseName)
	r.logf("prepare workspace=%s branch=%s repo=%s", workspace, env.Branch, r.cfg.Default.RepositoryURL)

	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return "", "", "", fmt.Errorf("create workspace: %w", err)
	}

	if err := gitClone(workspace, r.cfg.Default.RepositoryURL, env.Branch); err != nil {
		return "", "", "", err
	}

	sourceDir := workspace
	if dir := strings.TrimSpace(r.cfg.Default.DirToCopy); dir != "" {
		sourceDir = filepath.Join(workspace, dir)
	}

	info, err := os.Stat(sourceDir)
	if err != nil {
		return "", "", "", fmt.Errorf("stat dirToCopy %q: %w", sourceDir, err)
	}
	if !info.IsDir() {
		return "", "", "", fmt.Errorf("dirToCopy %q is not a directory", sourceDir)
	}

	r.logf("workspace ready source=%s", sourceDir)
	return workspace, releaseName, sourceDir, nil
}

func (r Runner) ensureRemoteDirs(env Environment, releasePath string) error {
	command := fmt.Sprintf("mkdir -p %s %s",
		shellQuote(filepath.ToSlash(filepath.Join(env.DeployTo, "releases"))),
		shellQuote(releasePath),
	)

	for _, server := range env.Servers {
		r.logf("ensure remote dirs server=%s release=%s", server, releasePath)
		if err := runSSHCommand(server, command, os.Stdout, os.Stderr); err != nil {
			return err
		}
	}

	return nil
}

func (r Runner) copyRelease(env Environment, sourceDir, releasePath string) error {
	for _, server := range env.Servers {
		r.logf("upload start server=%s source=%s target=%s", server, sourceDir, releasePath)
		fileCount := 0
		if err := copyDir(sourceDir, func(localPath, relPath string, entry os.DirEntry) error {
			remoteTarget := filepath.ToSlash(filepath.Join(releasePath, relPath))
			if entry.IsDir() {
				return runSSHCommand(server, "mkdir -p "+shellQuote(remoteTarget), os.Stdout, os.Stderr)
			}

			fileCount++
			r.logf("upload file server=%s path=%s", server, relPath)
			return uploadFile(server, localPath, remoteTarget)
		}, r.cfg.Default.Ignores); err != nil {
			return err
		}
		r.logf("upload done server=%s files=%d", server, fileCount)
	}

	return nil
}

func (r Runner) updateCurrentSymlink(env Environment, releasePath string) error {
	command := fmt.Sprintf("ln -sfn %s %s",
		shellQuote(releasePath),
		shellQuote(filepath.ToSlash(filepath.Join(env.DeployTo, "current"))),
	)

	for _, server := range env.Servers {
		r.logf("update current symlink server=%s current=%s", server, filepath.ToSlash(filepath.Join(env.DeployTo, "current")))
		if err := runSSHCommand(server, command, os.Stdout, os.Stderr); err != nil {
			return err
		}
	}

	return nil
}

func (r Runner) cleanupReleases(env Environment) error {
	releasesDir := filepath.ToSlash(filepath.Join(env.DeployTo, "releases"))

	for _, server := range env.Servers {
		r.logf("check old releases server=%s dir=%s keep=%d", server, releasesDir, r.cfg.Default.KeepReleases)
		entries, err := listRemoteDirs(server, releasesDir)
		if err != nil {
			return err
		}

		if len(entries) <= r.cfg.Default.KeepReleases {
			r.logf("skip cleanup server=%s releases=%d", server, len(entries))
			continue
		}

		sort.Strings(entries)
		toDelete := entries[:len(entries)-r.cfg.Default.KeepReleases]
		for _, entry := range toDelete {
			r.logf("delete old release server=%s release=%s", server, entry)
			command := "rm -rf " + shellQuote(filepath.ToSlash(filepath.Join(releasesDir, entry)))
			if err := runSSHCommand(server, command, os.Stdout, os.Stderr); err != nil {
				return err
			}
		}
	}

	return nil
}

func (r Runner) environment() (Environment, error) {
	env, ok := r.cfg.Environments[r.env]
	if !ok {
		return Environment{}, fmt.Errorf("environment %q not found", r.env)
	}
	if len(env.Servers) == 0 {
		return Environment{}, errors.New("environment has no servers configured")
	}
	return env, nil
}

func (r Runner) logf(format string, args ...any) {
	log.Printf("[shipit][%s] %s", r.env, fmt.Sprintf(format, args...))
}

func gitClone(workspace, repo, branch string) error {
	args := []string{"clone", "--depth", "1"}
	if strings.TrimSpace(branch) != "" {
		args = append(args, "--branch", branch, "--single-branch")
	}
	args = append(args, repo, workspace)

	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}
	return nil
}

func copyDir(root string, fn func(localPath, relPath string, entry os.DirEntry) error, ignores []string) error {
	ignoreSet := make(map[string]struct{}, len(ignores))
	for _, item := range ignores {
		ignoreSet[item] = struct{}{}
	}

	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)

		parts := strings.Split(relPath, "/")
		for _, part := range parts {
			if _, ignored := ignoreSet[part]; ignored {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		return fn(path, relPath, d)
	})
}

func runSSHCommand(server, command string, stdout, stderr io.Writer) error {
	log.Printf("[ssh][%s] %s", server, command)
	client, err := sshClient(server)
	if err != nil {
		return err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("create ssh session: %w", err)
	}
	defer session.Close()

	session.Stdout = stdout
	session.Stderr = stderr
	if err := session.Run(command); err != nil {
		return fmt.Errorf("run remote command on %s: %w", server, err)
	}
	return nil
}

func uploadFile(server, localPath, remotePath string) error {
	content, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("read local file %s: %w", localPath, err)
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("stat local file %s: %w", localPath, err)
	}

	client, err := sshClient(server)
	if err != nil {
		return err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("create ssh session: %w", err)
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return fmt.Errorf("open scp stdin: %w", err)
	}

	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	if err := session.Start("scp -qt " + shellQuote(filepath.Dir(remotePath))); err != nil {
		return fmt.Errorf("start scp: %w", err)
	}

	mode := fmt.Sprintf("%04o", info.Mode().Perm())
	fileName := filepath.Base(remotePath)
	if _, err := fmt.Fprintf(stdin, "C%s %d %s\n", mode, len(content), fileName); err != nil {
		return fmt.Errorf("write scp header: %w", err)
	}
	if _, err := stdin.Write(content); err != nil {
		return fmt.Errorf("write scp content: %w", err)
	}
	if _, err := stdin.Write([]byte{0}); err != nil {
		return fmt.Errorf("write scp terminator: %w", err)
	}
	if err := stdin.Close(); err != nil {
		return fmt.Errorf("close scp stdin: %w", err)
	}

	if err := session.Wait(); err != nil {
		return fmt.Errorf("finish scp: %w", err)
	}
	return nil
}

func listRemoteDirs(server, remoteDir string) ([]string, error) {
	client, err := sshClient(server)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("create ssh session: %w", err)
	}
	defer session.Close()

	output, err := session.Output("find " + shellQuote(remoteDir) + " -mindepth 1 -maxdepth 1 -type d -printf '%f\n'")
	if err != nil {
		return nil, fmt.Errorf("list remote dirs: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var dirs []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			dirs = append(dirs, line)
		}
	}
	return dirs, nil
}

func readRemoteSymlink(server, remotePath string) (string, error) {
	client, err := sshClient(server)
	if err != nil {
		return "", err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("create ssh session: %w", err)
	}
	defer session.Close()

	output, err := session.Output("readlink " + shellQuote(remotePath))
	if err != nil {
		return "", fmt.Errorf("read remote symlink %s on %s: %w", remotePath, server, err)
	}
	return strings.TrimSpace(string(output)), nil
}

func sshClient(server string) (*ssh.Client, error) {
	user, host, err := parseServer(server)
	if err != nil {
		return nil, err
	}

	signer, err := loadSigner()
	if err != nil {
		return nil, err
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	client, err := ssh.Dial("tcp", host+":22", config)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", server, err)
	}
	return client, nil
}

func parseServer(server string) (user, host string, err error) {
	parts := strings.Split(server, "@")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("server %q must be user@host", server)
	}
	return parts[0], parts[1], nil
}

func loadSigner() (ssh.Signer, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}

	keyPath := filepath.Join(home, ".ssh", "id_rsa")
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read ssh key %s: %w", keyPath, err)
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("parse ssh key %s: %w", keyPath, err)
	}
	return signer, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func indexOf(values []string, target string) int {
	for i, value := range values {
		if value == target {
			return i
		}
	}
	return -1
}
