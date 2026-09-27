/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package archive

import (
	"errors"
	"os"
	"path/filepath"
)

var errTooManyLinks = errors.New("too many links")

type fsRootPathResult struct {
	path                         string
	followedAbsoluteLink         bool
	relativeEscapeBeforeAbsolute bool
}

// resolveFSRootPath joins a path with a root, evaluating and bounding any
// symlink to the root directory (chroot-like semantics). Absolute symlinks
// are resolved relative to root, not the host root.
//
// The result also records whether an absolute symlink was followed, and
// whether a relative symlink escaped root before any absolute symlink was
// followed, so that callers can reject such paths instead of silently
// clamping them to root.
func resolveFSRootPath(root, path string) (fsRootPathResult, error) {
	result := fsRootPathResult{path: root}
	if path == "" {
		return result, nil
	}
	var linksWalked int // to protect against cycles
	for {
		i := linksWalked
		newpath, err := walkLinks(root, path, &linksWalked, &result)
		if err != nil {
			return fsRootPathResult{}, err
		}
		path = newpath
		if i == linksWalked {
			newpath = filepath.Join(string(os.PathSeparator), newpath)
			if path == newpath {
				result.path = filepath.Join(root, newpath)
				return result, nil
			}
			path = newpath
		}
	}
}

func walkLink(root, path string, linksWalked *int, result *fsRootPathResult) (newpath string, islink bool, err error) {
	if *linksWalked > 255 {
		return "", false, errTooManyLinks
	}

	path = filepath.Join(string(os.PathSeparator), path)
	if path == string(os.PathSeparator) {
		return path, false, nil
	}
	realPath := filepath.Join(root, path)

	fi, err := os.Lstat(realPath)
	if err != nil {
		// If path does not yet exist, treat as non-symlink
		if os.IsNotExist(err) {
			return path, false, nil
		}
		return "", false, err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return path, false, nil
	}
	newpath, err = os.Readlink(realPath)
	if err != nil {
		return "", false, err
	}
	if filepath.IsAbs(newpath) {
		result.followedAbsoluteLink = true
	} else if !result.followedAbsoluteLink {
		// Record a relative escape from root that happens before any
		// absolute link is followed, so that it is not silently clamped
		// to root by the chroot-like resolution.
		relativeDir, err := filepath.Rel(string(os.PathSeparator), filepath.Dir(path))
		if err != nil {
			return "", false, err
		}

		resolved := filepath.Join(relativeDir, newpath)
		if resolved != "." && !filepath.IsLocal(resolved) {
			result.relativeEscapeBeforeAbsolute = true
		}
	}

	*linksWalked++
	return newpath, true, nil
}

func walkLinks(root, path string, linksWalked *int, result *fsRootPathResult) (string, error) {
	switch dir, file := filepath.Split(path); {
	case dir == "":
		newpath, _, err := walkLink(root, file, linksWalked, result)
		return newpath, err
	case file == "":
		if os.IsPathSeparator(dir[len(dir)-1]) {
			if dir == string(os.PathSeparator) {
				return dir, nil
			}
			return walkLinks(root, dir[:len(dir)-1], linksWalked, result)
		}
		newpath, _, err := walkLink(root, dir, linksWalked, result)
		return newpath, err

	default:
		newdir, err := walkLinks(root, dir, linksWalked, result)
		if err != nil {
			return "", err
		}
		newpath, islink, err := walkLink(root, filepath.Join(newdir, file), linksWalked, result)
		if err != nil {
			return "", err
		}
		if !islink || filepath.IsAbs(newpath) {
			return newpath, nil
		}
		return filepath.Join(newdir, newpath), nil
	}
}
