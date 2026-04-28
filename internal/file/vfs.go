package file

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// FileNode 是本地虚拟文件树的节点。
type FileNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	IsFolder bool        `json:"is_folder"`
	Size     int64       `json:"size"`
	Hash     string      `json:"hash"`
	Children []*FileNode `json:"children,omitempty"`
}

// Engine 是 file 层实现，负责共享目录元数据、分块读写与断点存储。
type Engine struct {
	mu          sync.RWMutex
	sharedRoots map[string]struct{}
	runtime     *runtimeState
}

// NewEngine 创建 file 层引擎。
func NewEngine() *Engine {
	return &Engine{
		sharedRoots: make(map[string]struct{}),
		runtime:     newRuntimeState(),
	}
}

// ShareDirectory 添加一个共享目录根。
func (e *Engine) ShareDirectory(absPath string) error {
	root, err := normalizeDir(absPath)
	if err != nil {
		return err
	}

	e.mu.Lock()
	e.sharedRoots[root] = struct{}{}
	e.mu.Unlock()
	if err := e.ensureRootWatcher(root); err != nil {
		e.mu.Lock()
		delete(e.sharedRoots, root)
		e.mu.Unlock()
		return err
	}
	e.invalidateTreeCache(root)
	return nil
}

// UnshareDirectory 取消共享目录并释放对应 watcher。
func (e *Engine) UnshareDirectory(absPath string) error {
	root, err := normalizeDir(absPath)
	if err != nil {
		return err
	}

	e.mu.Lock()
	delete(e.sharedRoots, root)
	e.mu.Unlock()
	e.invalidateTreeCache(root)
	e.stopRootWatcher(root)
	return nil
}

// Close 释放 file 层运行时资源（watcher / goroutine）。
func (e *Engine) Close() error {
	runtime := e.getRuntime()
	if runtime == nil {
		return nil
	}

	runtime.watchMu.Lock()
	roots := make([]string, 0, len(runtime.watchers))
	for root := range runtime.watchers {
		roots = append(roots, root)
	}
	runtime.watchMu.Unlock()

	for _, root := range roots {
		e.stopRootWatcher(root)
	}
	return nil
}

// GetLocalTree 返回当前所有共享目录的虚拟树。
func (e *Engine) GetLocalTree() ([]*FileNode, error) {
	e.mu.RLock()
	roots := make([]string, 0, len(e.sharedRoots))
	for root := range e.sharedRoots {
		roots = append(roots, root)
	}
	e.mu.RUnlock()

	sort.Strings(roots)
	out := make([]*FileNode, 0, len(roots))
	for _, root := range roots {
		node, err := e.getOrBuildTree(root)
		if err != nil {
			return nil, err
		}
		out = append(out, node)
	}
	return out, nil
}

// GetVirtualTree 兼容 app.FileEngine 接口。
func (e *Engine) GetVirtualTree(rootPath string) (interface{}, error) {
	if strings.TrimSpace(rootPath) == "" {
		return e.GetLocalTree()
	}

	root, err := normalizeDir(rootPath)
	if err != nil {
		return nil, err
	}
	return e.getOrBuildTree(root)
}

// GetDirectoryChildren 按需加载目录下一层节点，不做全量递归扫描。
func (e *Engine) GetDirectoryChildren(absPath string) ([]*FileNode, error) {
	root, err := normalizeDir(absPath)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read dir failed: %w", err)
	}

	children := make([]*FileNode, 0, len(entries))
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil, fmt.Errorf("read entry info failed: %w", infoErr)
		}
		children = append(children, &FileNode{
			Name:     entry.Name(),
			Path:     filepath.Join(root, entry.Name()),
			IsFolder: entry.IsDir(),
			Size:     info.Size(),
		})
	}
	sort.Slice(children, func(i, j int) bool {
		if children[i].IsFolder != children[j].IsFolder {
			return children[i].IsFolder
		}
		return strings.ToLower(children[i].Name) < strings.ToLower(children[j].Name)
	})
	return children, nil
}

func (e *Engine) getOrBuildTree(root string) (*FileNode, error) {
	runtime := e.getRuntime()
	if runtime == nil {
		return buildTree(root)
	}

	runtime.cacheMu.RLock()
	cache, ok := runtime.treeCache[root]
	runtime.cacheMu.RUnlock()
	if ok {
		return cloneNode(cache.node), nil
	}

	node, err := buildTree(root)
	if err != nil {
		return nil, err
	}

	runtime.cacheMu.Lock()
	runtime.treeCache[root] = treeCacheEntry{node: cloneNode(node)}
	runtime.cacheMu.Unlock()
	return node, nil
}

func (e *Engine) invalidateTreeCache(root string) {
	runtime := e.getRuntime()
	if runtime == nil {
		return
	}
	runtime.cacheMu.Lock()
	delete(runtime.treeCache, root)
	runtime.cacheMu.Unlock()
}

func (e *Engine) invalidateHashCachePath(path string) {
	absPath, err := filepathAbs(path)
	if err != nil {
		return
	}

	runtime := e.getRuntime()
	if runtime == nil {
		return
	}

	runtime.cacheMu.Lock()
	delete(runtime.hashCache, absPath)
	prefix := absPath + string(os.PathSeparator)
	for cachedPath := range runtime.hashCache {
		if strings.HasPrefix(cachedPath, prefix) {
			delete(runtime.hashCache, cachedPath)
		}
	}
	runtime.cacheMu.Unlock()
}

func cloneNode(node *FileNode) *FileNode {
	if node == nil {
		return nil
	}
	copyNode := &FileNode{
		Name:     node.Name,
		Path:     node.Path,
		IsFolder: node.IsFolder,
		Size:     node.Size,
		Hash:     node.Hash,
	}
	if len(node.Children) > 0 {
		copyNode.Children = make([]*FileNode, 0, len(node.Children))
		for _, child := range node.Children {
			copyNode.Children = append(copyNode.Children, cloneNode(child))
		}
	}
	return copyNode
}

func (e *Engine) ensureRootWatcher(root string) error {
	runtime := e.getRuntime()
	if runtime == nil {
		return nil
	}

	runtime.watchMu.Lock()
	if _, exists := runtime.watchers[root]; exists {
		runtime.watchMu.Unlock()
		return nil
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		runtime.watchMu.Unlock()
		return fmt.Errorf("create fs watcher failed: %w", err)
	}
	dirWatcher := &rootWatcher{
		watcher: watcher,
		done:    make(chan struct{}),
	}
	runtime.watchers[root] = dirWatcher
	runtime.watchMu.Unlock()

	if err := walkAndWatch(root, watcher); err != nil {
		_ = watcher.Close()
		runtime.watchMu.Lock()
		delete(runtime.watchers, root)
		runtime.watchMu.Unlock()
		return err
	}

	go e.runRootWatcher(root, dirWatcher)
	return nil
}

func (e *Engine) runRootWatcher(root string, rw *rootWatcher) {
	for {
		select {
		case event, ok := <-rw.watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename|fsnotify.Chmod) == 0 {
				continue
			}
			e.invalidateTreeCache(root)
			e.invalidateHashCachePath(event.Name)
			e.publishDirChanged(root)

			if event.Op&fsnotify.Create != 0 {
				info, err := os.Stat(event.Name)
				if err == nil && info.IsDir() {
					_ = walkAndWatch(event.Name, rw.watcher)
				}
			}
		case _, ok := <-rw.watcher.Errors:
			if !ok {
				return
			}
		case <-rw.done:
			_ = rw.watcher.Close()
			return
		}
	}
}

func (e *Engine) stopRootWatcher(root string) {
	runtime := e.getRuntime()
	if runtime == nil {
		return
	}

	runtime.watchMu.Lock()
	rw, ok := runtime.watchers[root]
	if ok {
		delete(runtime.watchers, root)
	}
	runtime.watchMu.Unlock()
	if ok {
		rw.once.Do(func() {
			close(rw.done)
		})
	}
}

func walkAndWatch(root string, watcher *fsnotify.Watcher) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if addErr := watcher.Add(path); addErr != nil {
			if errorsIsPathNotFound(addErr) {
				return nil
			}
			return addErr
		}
		return nil
	})
}

func errorsIsPathNotFound(err error) bool {
	return err != nil && os.IsNotExist(err)
}

func normalizeDir(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("directory path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path failed: %w", err)
	}
	clean := filepath.Clean(abs)
	info, err := os.Stat(clean)
	if err != nil {
		return "", fmt.Errorf("stat directory failed: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory: %s", clean)
	}
	return clean, nil
}

func buildTree(root string) (*FileNode, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat root failed: %w", err)
	}
	node := &FileNode{
		Name:     info.Name(),
		Path:     root,
		IsFolder: info.IsDir(),
		Size:     info.Size(),
	}
	if !info.IsDir() {
		return node, nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read dir failed: %w", err)
	}

	children := make([]*FileNode, 0, len(entries))
	for _, entry := range entries {
		childPath := filepath.Join(root, entry.Name())
		childNode, childErr := buildTreeFromEntry(childPath, entry)
		if childErr != nil {
			return nil, childErr
		}
		children = append(children, childNode)
	}

	sort.Slice(children, func(i, j int) bool {
		if children[i].IsFolder != children[j].IsFolder {
			return children[i].IsFolder
		}
		return strings.ToLower(children[i].Name) < strings.ToLower(children[j].Name)
	})
	node.Children = children
	return node, nil
}

func buildTreeFromEntry(path string, entry fs.DirEntry) (*FileNode, error) {
	info, err := entry.Info()
	if err != nil {
		return nil, fmt.Errorf("read entry info failed: %w", err)
	}
	node := &FileNode{
		Name:     entry.Name(),
		Path:     path,
		IsFolder: entry.IsDir(),
		Size:     info.Size(),
	}
	if !entry.IsDir() {
		return node, nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("read dir failed: %w", err)
	}
	node.Children = make([]*FileNode, 0, len(entries))
	for _, child := range entries {
		childPath := filepath.Join(path, child.Name())
		childNode, childErr := buildTreeFromEntry(childPath, child)
		if childErr != nil {
			return nil, childErr
		}
		node.Children = append(node.Children, childNode)
	}

	sort.Slice(node.Children, func(i, j int) bool {
		if node.Children[i].IsFolder != node.Children[j].IsFolder {
			return node.Children[i].IsFolder
		}
		return strings.ToLower(node.Children[i].Name) < strings.ToLower(node.Children[j].Name)
	})
	return node, nil
}
