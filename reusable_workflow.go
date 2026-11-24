package actionlint

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.yaml.in/yaml/v4"
)

func expectedMapping(where string, n *yaml.Node) error {
	return fmt.Errorf(
		"yaml: %s must be mapping node but %s node was found at line:%d, col:%d",
		where,
		nodeKindName(n.Kind),
		n.Line,
		n.Column,
	)
}

// ReusableWorkflowMetadataInput is an input metadata for validating local reusable workflow file.
type ReusableWorkflowMetadataInput struct {
	// Name is a name of the input defined in the reusable workflow.
	Name string
	// Required is true when 'required' field of the input is set to true and no default value is set.
	Required bool
	// Type is a type of the input. When the input type is unknown, 'any' type is set.
	Type ExprType
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (input *ReusableWorkflowMetadataInput) UnmarshalYAML(n *yaml.Node) error {
	type metadata struct {
		Required bool    `yaml:"required"`
		Default  *string `yaml:"default"`
		Type     string  `yaml:"type"`
	}

	var md metadata
	if err := n.Decode(&md); err != nil {
		return err
	}

	input.Required = md.Required && md.Default == nil
	switch md.Type {
	case "boolean":
		input.Type = BoolType{}
	case "number":
		input.Type = NumberType{}
	case "string":
		input.Type = StringType{}
	default:
		input.Type = AnyType{}
	}

	return nil
}

// ReusableWorkflowMetadataInputs is a map from input name to reusable wokflow input metadata. The
// keys are in lower case since input names of workflow calls are case insensitive.
type ReusableWorkflowMetadataInputs map[string]*ReusableWorkflowMetadataInput

// UnmarshalYAML implements yaml.Unmarshaler.
func (inputs *ReusableWorkflowMetadataInputs) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return expectedMapping("on.workflow_call.inputs", n)
	}

	md := make(ReusableWorkflowMetadataInputs, len(n.Content)/2)
	for i := 0; i < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]

		var m ReusableWorkflowMetadataInput
		if err := v.Decode(&m); err != nil {
			return err
		}
		m.Name = k.Value
		if m.Type == nil {
			m.Type = AnyType{} // Reach here when `v` is null node
		}

		md[strings.ToLower(k.Value)] = &m
	}

	*inputs = md
	return nil
}

// ReusableWorkflowMetadataSecret is a secret metadata for validating local reusable workflow file.
type ReusableWorkflowMetadataSecret struct {
	// Name is a name of the secret in the reusable workflow.
	Name string
	// Required indicates whether the secret is required by its reusable workflow. When this value
	// is true, workflow calls must set this secret unless secrets are not inherited.
	Required bool `yaml:"required"`
}

// ReusableWorkflowMetadataSecrets is a map from secret name to reusable wokflow secret metadata.
// The keys are in lower case since secret names of workflow calls are case insensitive.
type ReusableWorkflowMetadataSecrets map[string]*ReusableWorkflowMetadataSecret

// UnmarshalYAML implements yaml.Unmarshaler.
func (secrets *ReusableWorkflowMetadataSecrets) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return expectedMapping("on.workflow_call.secrets", n)
	}

	md := make(ReusableWorkflowMetadataSecrets, len(n.Content)/2)
	for i := 0; i < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]

		var s ReusableWorkflowMetadataSecret
		if err := v.Decode(&s); err != nil {
			return err
		}
		s.Name = k.Value

		md[strings.ToLower(k.Value)] = &s
	}

	*secrets = md
	return nil
}

// ReusableWorkflowMetadataOutput is an output metadata for validating local reusable workflow file.
type ReusableWorkflowMetadataOutput struct {
	// Name is a name of the output in the reusable workflow.
	Name string
}

// ReusableWorkflowMetadataOutputs is a map from output name to reusable wokflow output metadata.
// The keys are in lower case since output names of workflow calls are case insensitive.
type ReusableWorkflowMetadataOutputs map[string]*ReusableWorkflowMetadataOutput

// UnmarshalYAML implements yaml.Unmarshaler.
func (outputs *ReusableWorkflowMetadataOutputs) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return expectedMapping("on.workflow_call.outputs", n)
	}

	md := make(ReusableWorkflowMetadataOutputs, len(n.Content)/2)
	for i := 0; i < len(n.Content); i += 2 {
		k := n.Content[i]
		md[strings.ToLower(k.Value)] = &ReusableWorkflowMetadataOutput{
			Name: k.Value,
		}
	}

	*outputs = md
	return nil
}

// ReusableWorkflowMetadata is metadata to validate local reusable workflows. This struct does not
// contain all metadata from YAML file. It only contains metadata which is necessary to validate
// reusable workflow files by actionlint.
type ReusableWorkflowMetadata struct {
	Inputs  ReusableWorkflowMetadataInputs  `yaml:"inputs"`
	Outputs ReusableWorkflowMetadataOutputs `yaml:"outputs"`
	Secrets ReusableWorkflowMetadataSecrets `yaml:"secrets"`
}

// LocalReusableWorkflowCache is a cache for local reusable workflow metadata files. It avoids find/read/parse
// local reusable workflow YAML files. This cache is dedicated for a single project (repository)
// indicated by 'proj' field. One LocalReusableWorkflowCache instance needs to be created per one
// project.
type LocalReusableWorkflowCache struct {
	mu    sync.RWMutex
	proj  *Project // maybe nil
	cache map[string]*ReusableWorkflowMetadata
	cwd   string
	dbg   io.Writer
	cfg   *Config // maybe nil
}

func (c *LocalReusableWorkflowCache) debug(format string, args ...interface{}) {
	if c.dbg == nil {
		return
	}
	format = "[LocalReusableWorkflowCache] " + format + "\n"
	fmt.Fprintf(c.dbg, format, args...)
}

func (c *LocalReusableWorkflowCache) readCache(key string) (*ReusableWorkflowMetadata, bool) {
	c.mu.RLock()
	m, ok := c.cache[key]
	c.mu.RUnlock()
	return m, ok
}

func (c *LocalReusableWorkflowCache) writeCache(key string, val *ReusableWorkflowMetadata) {
	c.mu.Lock()
	c.cache[key] = val
	c.mu.Unlock()
}

// SetConfig sets an effective configuration to the cache. The configuration is used for resolving
// snapshots of remote reusable workflow files. When this method is never called or nil is set,
// the configuration of the project is used instead. Calling this method is thread-safe.
func (c *LocalReusableWorkflowCache) SetConfig(cfg *Config) {
	c.mu.Lock()
	c.cfg = cfg
	c.mu.Unlock()
}

func (c *LocalReusableWorkflowCache) getConfig() *Config {
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	if cfg != nil {
		return cfg
	}
	if c.proj != nil {
		return c.proj.Config()
	}
	return nil
}

// parseRemoteWorkflowCallSpec parses a remote reusable workflow call spec
// "owner/repo/path/to/workflow.yml@ref" into its components. The ref is separated from the
// workflow path at the first "@" following the same rule as isWorkflowCallUsesRepoFormat.
func parseRemoteWorkflowCallSpec(spec string) (owner, repo, wpath, ref string, ok bool) {
	if strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "/") {
		return "", "", "", "", false
	}

	i := strings.IndexRune(spec, '/')
	if i <= 0 {
		return "", "", "", "", false
	}
	owner, rest := spec[:i], spec[i+1:]

	i = strings.IndexRune(rest, '/')
	if i <= 0 {
		return "", "", "", "", false
	}
	repo, rest = rest[:i], rest[i+1:]

	i = strings.IndexRune(rest, '@')
	if i <= 0 || i == len(rest)-1 {
		return "", "", "", "", false
	}
	wpath, ref = rest[:i], rest[i+1:]

	return owner, repo, wpath, ref, true
}

// snapshotFilePath resolves a file path of the snapshot for the given remote reusable workflow
// call spec such as "owner/repo/path/to/workflow.yml@ref". The second return value is false when
// no snapshots directory is configured, the spec is not a remote workflow call, or the resolved
// path escapes the snapshots directory.
func (c *LocalReusableWorkflowCache) snapshotFilePath(spec string) (string, bool) {
	cfg := c.getConfig()
	if c.proj == nil || cfg == nil || cfg.ReusableWorkflows == nil || cfg.ReusableWorkflows.SnapshotsDir == "" {
		return "", false
	}

	owner, repo, wpath, ref, ok := parseRemoteWorkflowCallSpec(spec)
	if !ok {
		return "", false
	}

	// GitHub treats owner and repository names case-insensitively so they are normalized to
	// lower case. On the other hand, the workflow path and the ref are case-sensitive so they
	// are used as-is. The ref is matched exactly; a snapshot for "@v1" is never used for a call
	// with "@v1.2.3".
	owner = strings.ToLower(owner)
	repo = strings.ToLower(repo)

	base := filepath.Join(c.proj.RootDir(), filepath.FromSlash(cfg.ReusableWorkflows.SnapshotsDir))
	// Note: filepath.Join cleans the result so ".." path elements in the workflow path could
	// escape the snapshots directory. Such paths are rejected by the check below.
	p := filepath.Join(base, owner, repo, filepath.FromSlash(wpath)+"@"+ref)
	if !strings.HasPrefix(p, base+string(os.PathSeparator)) {
		c.debug("Snapshot path %q for %q is out of the snapshots directory %q", p, spec, base)
		return "", false
	}
	return p, true
}

// snapshotNote returns a message suffix which tells that the metadata was resolved from a local
// snapshot file. It returns an empty string when the spec is not a remote reusable workflow call
// or no snapshots directory is configured.
func (c *LocalReusableWorkflowCache) snapshotNote(spec string) string {
	if p, ok := c.snapshotFilePath(spec); ok {
		return fmt.Sprintf(". the metadata was read from snapshot %q", p)
	}
	return ""
}

// FindMetadata finds/parses a reusable workflow metadata located by the 'spec' argument. When project
// is not set to 'proj' field, this method immediately returns with nil.
//
// When the spec starts with "./", the metadata is read from the local reusable workflow file in
// the project. Otherwise the spec is considered a remote workflow call
// "owner/repo/path/to/workflow.yml@ref" and the metadata is read from a local snapshot of the
// remote workflow file when a snapshots directory is configured with the "reusable-workflows"
// configuration. When no snapshot is found for the remote call, this method returns with nil
// (or an error when the strict mode is enabled) so that the call is skipped as before.
//
// Note that an error is not cached. At first search, let's say this method returned an error since
// the reusable workflow is invalid. In this case, calling this method with the same spec later will
// not return the error again. It just will return nil. This behavior prevents repeating to report
// the same error from multiple places.
//
// Calling this method is thread-safe.
func (c *LocalReusableWorkflowCache) FindMetadata(spec string) (*ReusableWorkflowMetadata, error) {
	if c.proj == nil || ContainsExpression(spec) {
		return nil, nil
	}

	if m, ok := c.readCache(spec); ok {
		c.debug("Cache hit for %s: %v", spec, m)
		return m, nil
	}

	if strings.HasPrefix(spec, "./") {
		return c.findLocalMetadata(spec)
	}
	return c.findRemoteSnapshotMetadata(spec)
}

func (c *LocalReusableWorkflowCache) findLocalMetadata(spec string) (*ReusableWorkflowMetadata, error) {
	file := filepath.Join(c.proj.RootDir(), filepath.FromSlash(spec))
	src, err := os.ReadFile(file)
	if err != nil {
		c.writeCache(spec, nil) // Remember the workflow file was not found
		return nil, fmt.Errorf("could not read reusable workflow file for %q: %w", spec, err)
	}

	m, err := parseReusableWorkflowMetadata(src)
	if err != nil {
		c.writeCache(spec, nil) // Remember the workflow file was invalid
		msg := strings.ReplaceAll(err.Error(), "\n", " ")
		return nil, fmt.Errorf("error while parsing reusable workflow %q: %s", spec, msg)
	}

	c.debug("New reusable workflow metadata at %s: %v", file, m)
	c.writeCache(spec, m)
	return m, nil
}

func (c *LocalReusableWorkflowCache) findRemoteSnapshotMetadata(spec string) (*ReusableWorkflowMetadata, error) {
	file, ok := c.snapshotFilePath(spec)
	if !ok {
		return nil, nil
	}

	st, err := os.Lstat(file)
	if err != nil {
		c.writeCache(spec, nil) // Remember the snapshot was not found
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("could not read snapshot %q for remote reusable workflow %q: %w", file, spec, err)
		}
		if cfg := c.getConfig(); cfg != nil && cfg.ReusableWorkflows != nil && cfg.ReusableWorkflows.Strict {
			return nil, fmt.Errorf(
				"no snapshot was found for remote reusable workflow %q. save the workflow file of the remote repository as %q to check this call. see https://github.com/rhysd/actionlint/blob/main/docs/config.md for more details",
				spec,
				file,
			)
		}
		// By default, remote workflow calls with no snapshot are silently skipped
		return nil, nil
	}
	if st.Mode()&os.ModeSymlink != 0 {
		c.writeCache(spec, nil) // Remember the snapshot was invalid
		return nil, fmt.Errorf(
			"snapshot %q for remote reusable workflow %q is a symbolic link. snapshot files must be regular files since snapshots are untrusted data",
			file,
			spec,
		)
	}
	if !st.Mode().IsRegular() {
		c.writeCache(spec, nil) // Remember the snapshot was invalid
		return nil, fmt.Errorf("snapshot %q for remote reusable workflow %q is not a regular file", file, spec)
	}

	src, err := os.ReadFile(file)
	if err != nil {
		c.writeCache(spec, nil) // Remember the snapshot was not readable
		return nil, fmt.Errorf("could not read snapshot %q for remote reusable workflow %q: %w", file, spec, err)
	}

	m, err := parseReusableWorkflowMetadata(src)
	if err != nil {
		c.writeCache(spec, nil) // Remember the snapshot was invalid
		msg := strings.ReplaceAll(err.Error(), "\n", " ")
		return nil, fmt.Errorf("error while parsing snapshot %q for remote reusable workflow %q: %s", file, spec, msg)
	}

	c.debug("New reusable workflow metadata from snapshot %s: %v", file, m)
	c.writeCache(spec, m)
	return m, nil
}

func (c *LocalReusableWorkflowCache) convWorkflowPathToSpec(p string) (string, bool) {
	if c.proj == nil {
		return "", false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(c.cwd, p)
	}
	r := c.proj.RootDir()
	if !strings.HasPrefix(p, r) {
		return "", false
	}
	p, err := filepath.Rel(r, p)
	if err != nil {
		return "", false // Unreachable
	}
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "./") {
		p = "./" + p
	}
	return p, true
}

// WriteWorkflowCallEvent writes reusable workflow metadata by converting from WorkflowCallEvent AST
// node. The 'wpath' parameter is a path to the workflow file of the AST, which is a relative to the
// project root directory or an absolute path.
// This method does nothing when (1) no project is set, (2) it could not convert the workflow path
// to workflow call spec, (3) some cache for the workflow is already existing.
// This method is thread safe.
func (c *LocalReusableWorkflowCache) WriteWorkflowCallEvent(wpath string, event *WorkflowCallEvent) {
	// Convert workflow path to workflow call spec
	spec, ok := c.convWorkflowPathToSpec(wpath)
	if !ok {
		return
	}
	c.debug("Workflow call spec from workflow path %s: %s", wpath, spec)

	c.mu.RLock()
	_, ok = c.cache[spec]
	c.mu.RUnlock()
	if ok {
		return
	}

	m := &ReusableWorkflowMetadata{
		Inputs:  ReusableWorkflowMetadataInputs{},
		Outputs: ReusableWorkflowMetadataOutputs{},
		Secrets: ReusableWorkflowMetadataSecrets{},
	}

	for _, i := range event.Inputs {
		var t ExprType = AnyType{}
		switch i.Type {
		case WorkflowCallEventInputTypeBoolean:
			t = BoolType{}
		case WorkflowCallEventInputTypeNumber:
			t = NumberType{}
		case WorkflowCallEventInputTypeString:
			t = StringType{}
		}
		m.Inputs[i.ID] = &ReusableWorkflowMetadataInput{
			Type:     t,
			Required: i.Required != nil && i.Required.Value && i.Default == nil,
			Name:     i.Name.Value,
		}
	}

	for n, o := range event.Outputs {
		m.Outputs[n] = &ReusableWorkflowMetadataOutput{
			Name: o.Name.Value,
		}
	}

	for n, s := range event.Secrets {
		r := s.Required != nil && s.Required.Value
		m.Secrets[n] = &ReusableWorkflowMetadataSecret{
			Required: r,
			Name:     s.Name.Value,
		}
	}

	c.mu.Lock()
	c.cache[spec] = m
	c.mu.Unlock()

	c.debug("Workflow call metadata from workflow %s: %v", wpath, m)
}

func parseReusableWorkflowMetadata(src []byte) (*ReusableWorkflowMetadata, error) {
	type workflow struct {
		On yaml.Node `yaml:"on"`
	}

	var w workflow
	if err := yaml.Unmarshal(src, &w); err != nil {
		return nil, err // Unreachable
	}

	n := &w.On
	if n.Line == 0 && n.Column == 0 {
		return nil, fmt.Errorf("\"on:\" is not found")
	}

	switch n.Kind {
	case yaml.MappingNode:
		// on:
		//   workflow_call:
		for i := 0; i < len(n.Content); i += 2 {
			k := strings.ToLower(n.Content[i].Value)
			if k == "workflow_call" {
				var m ReusableWorkflowMetadata
				if err := n.Content[i+1].Decode(&m); err != nil {
					return nil, err
				}
				return &m, nil
			}
		}
	case yaml.ScalarNode:
		// on: workflow_call
		if v := strings.ToLower(n.Value); v == "workflow_call" {
			return &ReusableWorkflowMetadata{}, nil
		}
	case yaml.SequenceNode:
		// on: [workflow_call]
		for _, c := range n.Content {
			e := strings.ToLower(c.Value)
			if e == "workflow_call" {
				return &ReusableWorkflowMetadata{}, nil
			}
		}
	}

	return nil, fmt.Errorf("\"workflow_call\" event trigger is not found in \"on:\" at line:%d, column:%d", n.Line, n.Column)
}

// NewLocalReusableWorkflowCache creates a new LocalReusableWorkflowCache instance for the given
// project. 'cwd' is a current working directory as an absolute file path. The 'Local' means that
// the cache instance is project-local. It is not available across multiple projects.
func NewLocalReusableWorkflowCache(proj *Project, cwd string, dbg io.Writer) *LocalReusableWorkflowCache {
	return &LocalReusableWorkflowCache{
		proj:  proj,
		cache: map[string]*ReusableWorkflowMetadata{},
		cwd:   cwd,
		dbg:   dbg,
	}
}

func newNullLocalReusableWorkflowCache(dbg io.Writer) *LocalReusableWorkflowCache {
	// Null cache. Cache never hits. It is used when project is not found
	return &LocalReusableWorkflowCache{dbg: dbg}
}

// LocalReusableWorkflowCacheFactory is a factory object to create a LocalReusableWorkflowCache
// instance per project.
type LocalReusableWorkflowCacheFactory struct {
	caches map[string]*LocalReusableWorkflowCache
	cwd    string
	dbg    io.Writer
}

// NewLocalReusableWorkflowCacheFactory creates a new LocalReusableWorkflowCacheFactory instance.
func NewLocalReusableWorkflowCacheFactory(cwd string, dbg io.Writer) *LocalReusableWorkflowCacheFactory {
	return &LocalReusableWorkflowCacheFactory{map[string]*LocalReusableWorkflowCache{}, cwd, dbg}
}

// GetCache returns a new or existing LocalReusableWorkflowCache instance per project. When a instance
// was already created for the project, this method returns the existing instance. Otherwise it creates
// a new instance and returns it.
func (f *LocalReusableWorkflowCacheFactory) GetCache(p *Project) *LocalReusableWorkflowCache {
	if p == nil {
		return newNullLocalReusableWorkflowCache(f.dbg)
	}
	r := p.RootDir()
	if c, ok := f.caches[r]; ok {
		return c
	}
	c := NewLocalReusableWorkflowCache(p, f.cwd, f.dbg)
	f.caches[r] = c
	return c
}
