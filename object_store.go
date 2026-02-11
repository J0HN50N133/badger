/*
 * SPDX-FileCopyrightText: © 2017-2025 Istari Digital, Inc.
 * SPDX-License-Identifier: Apache-2.0
 */

package badger

import "context"

// ValueLogObjectStore defines the minimal object-store operations needed by
// vlog tiering MVP.
//
// objectKey is a logical object name (for example: 000123.vlog). The concrete
// mapping to bucket/prefix is implementation-defined.
type ValueLogObjectStore interface {
	UploadFile(ctx context.Context, localPath string, objectKey string) error
	DownloadFile(ctx context.Context, objectKey string, localPath string) error
	DeleteObject(ctx context.Context, objectKey string) error
}
