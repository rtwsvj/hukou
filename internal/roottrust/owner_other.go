//go:build !unix

package roottrust

import "os"

// ownerOfFileInfo reports -1 on platforms that do not expose file ownership;
// callers treat a negative result as "ownership unknown" and skip the owner
// check rather than guessing.
func ownerOfFileInfo(fi os.FileInfo) int {
	return -1
}
