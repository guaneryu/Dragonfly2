/*
 *     Copyright 2020 The Dragonfly Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package service

import (
	"context"
	"time"

	"d7y.io/dragonfly/v2/internal/dferrors"
	"d7y.io/dragonfly/v2/pkg/container/set"
	"d7y.io/dragonfly/v2/pkg/rpc/base"
	rpcscheduler "d7y.io/dragonfly/v2/pkg/rpc/scheduler"
	"d7y.io/dragonfly/v2/scheduler/config"
	"d7y.io/dragonfly/v2/scheduler/resource"
	"d7y.io/dragonfly/v2/scheduler/scheduler"
)

type Callback interface {
	ScheduleParent(context.Context, *resource.Peer, set.SafeSet)
	BeginOfPiece(context.Context, *resource.Peer)
	EndOfPiece(context.Context, *resource.Peer)
	PieceSuccess(context.Context, *resource.Peer, *rpcscheduler.PieceResult)
	PieceFail(context.Context, *resource.Peer, *rpcscheduler.PieceResult)
	PeerSuccess(context.Context, *resource.Peer)
	PeerFail(context.Context, *resource.Peer)
	PeerLeave(context.Context, *resource.Peer)
	TaskSuccess(context.Context, *resource.Peer, *resource.Task, *rpcscheduler.PeerResult)
	TaskFail(context.Context, *resource.Task)
}

type callback struct {
	// Resource interface
	resource resource.Resource

	// Scheduler interface
	scheduler scheduler.Scheduler

	// Scheduelr service config
	config *config.Config
}

func newCallback(cfg *config.Config, resource resource.Resource, scheduler scheduler.Scheduler) Callback {
	return &callback{
		config:    cfg,
		resource:  resource,
		scheduler: scheduler,
	}
}

// Repeat schedule parent for peer
func (c *callback) ScheduleParent(ctx context.Context, peer *resource.Peer, blocklist set.SafeSet) {
	var n int
	for {
		select {
		case <-ctx.Done():
			peer.Log.Infof("context was done")
			return
		default:
		}

		// Peer scheduling exceeds retry limit
		if n >= c.config.Scheduler.RetryLimit {
			if peer.Task.CanBackToSource() {
				if ok := peer.StopStream(dferrors.Newf(base.Code_SchedNeedBackSource, "peer scheduling exceeds the limit %d times", c.config.Scheduler.RetryLimit)); !ok {
					return
				}

				if err := peer.FSM.Event(resource.PeerEventDownloadFromBackToSource); err != nil {
					peer.Log.Errorf("peer fsm event failed: %v", err)
					return
				}

				if peer.Task.FSM.Is(resource.PeerStatePending) {
					if err := peer.Task.FSM.Event(resource.TaskEventDownload); err != nil {
						peer.Task.Log.Errorf("task fsm event failed: %v", err)
						return
					}
				}

				// If the peer downloads back-to-source, its parent needs to be deleted
				peer.DeleteParent()
				peer.Task.Log.Info("peer back to source successfully")
				return
			}

			// Handle peer failed
			if ok := peer.StopStream(dferrors.Newf(base.Code_SchedTaskStatusError, "peer scheduling exceeds the limit %d times", c.config.Scheduler.RetryLimit)); !ok {
				peer.Log.Error("stop stream failed")
			}
			return
		}

		if _, ok := c.scheduler.ScheduleParent(ctx, peer, blocklist); !ok {
			n++
			peer.Log.Infof("reschedule parent %d times failed", n)

			// Sleep to avoid hot looping
			time.Sleep(c.config.Scheduler.RetryInterval)
			continue
		}

		peer.Log.Infof("reschedule parent %d times successfully", n+1)
		return
	}
}

func (c *callback) BeginOfPiece(ctx context.Context, peer *resource.Peer) {
	switch peer.FSM.Current() {
	case resource.PeerStateBackToSource:
		// Back to the source download process, peer directly returns
		peer.Log.Info("peer back to source")
		return
	case resource.PeerStateReceivedSmall:
		// When the task is small,
		// the peer has already returned to the parent when registering
		if err := peer.FSM.Event(resource.PeerEventDownload); err != nil {
			peer.Log.Errorf("peer fsm event failed: %v", err)
			return
		}
	case resource.PeerStateReceivedNormal:
		// It’s not a case of back-to-source or small task downloading,
		// to help peer to schedule the parent node
		if err := peer.FSM.Event(resource.PeerEventDownload); err != nil {
			peer.Log.Errorf("peer fsm event failed: %v", err)
			return
		}
		blocklist := set.NewSafeSet()
		blocklist.Add(peer.ID)
		c.ScheduleParent(ctx, peer, blocklist)
	default:
		peer.Log.Errorf("peer state is %s when receive the begin of piece", peer.FSM.Current())
	}
}

func (c *callback) EndOfPiece(ctx context.Context, peer *resource.Peer) {}

func (c *callback) PieceFail(ctx context.Context, peer *resource.Peer, piece *rpcscheduler.PieceResult) {
	// Failed to download piece back-to-source
	if peer.FSM.Is(resource.PeerStateBackToSource) {
		peer.Log.Error("peer back to source finished with fail piece")
		return
	}

	// It’s not a case of back-to-source downloading failed,
	// to help peer to reschedule the parent node
	switch piece.Code {
	case base.Code_ClientWaitPieceReady:
		peer.Log.Info("receive code Code_ClientWaitPieceReady")
		return
	case base.Code_ClientPieceDownloadFail, base.Code_PeerTaskNotFound, base.Code_CDNTaskNotFound, base.Code_CDNError, base.Code_CDNTaskDownloadFail:
		peer.Log.Errorf("receive error code: %v", piece.Code)
		if parent, ok := c.resource.PeerManager().Load(piece.DstPid); ok && parent.FSM.Can(resource.PeerEventDownloadFailed) {
			if err := parent.FSM.Event(resource.PeerEventDownloadFailed); err != nil {
				peer.Log.Errorf("peer fsm event failed: %v", err)
				break
			}
		}
	case base.Code_ClientPieceRequestFail:
		peer.Log.Error("receive error code Code_ClientPieceRequestFail")
	default:
		peer.Log.Warnf("unknow report code: %v", piece.Code)
	}

	// Peer state is PeerStateRunning will be rescheduled
	if !peer.FSM.Is(resource.PeerStateRunning) {
		peer.Log.Infof("peer can not be rescheduled because peer state is %s", peer.FSM.Current())
		return
	}

	blocklist := set.NewSafeSet()
	if parent, ok := c.resource.PeerManager().Load(piece.DstPid); ok {
		blocklist.Add(parent.ID)
	}

	c.ScheduleParent(ctx, peer, blocklist)
}

func (c *callback) PieceSuccess(ctx context.Context, peer *resource.Peer, piece *rpcscheduler.PieceResult) {
	// Update peer piece info
	peer.Pieces.Set(uint(piece.PieceInfo.PieceNum))
	peer.PieceCosts.Add(piece.EndTime - piece.BeginTime)

	// When the peer downloads back-to-source,
	// piece downloads successfully updates the task piece info
	if peer.FSM.Is(resource.PeerStateBackToSource) {
		peer.Task.StorePiece(piece.PieceInfo)
	}
}

func (c *callback) PeerSuccess(ctx context.Context, peer *resource.Peer) {
	// If the peer type is tiny and back-to-source,
	// it need to directly download the tiny file and store the data in task DirectPiece
	if peer.FSM.Is(resource.PeerStateBackToSource) && peer.Task.SizeScope() == base.SizeScope_TINY {
		peer.Log.Info("peer state is PeerStateBackToSource and type is tiny file")
		data, err := peer.DownloadTinyFile(ctx)
		if err == nil && len(data) == int(peer.Task.ContentLength.Load()) {
			// Tiny file downloaded successfully
			peer.Task.DirectPiece = data
		} else {
			peer.Log.Warnf("download tiny file length %d is failed: %v", len(data), err)
		}
	}

	if err := peer.FSM.Event(resource.PeerEventDownloadSucceeded); err != nil {
		peer.Log.Errorf("peer fsm event failed: %v", err)
		return
	}
}

func (c *callback) PeerFail(ctx context.Context, peer *resource.Peer) {
	if err := peer.FSM.Event(resource.PeerEventDownloadFailed); err != nil {
		peer.Log.Errorf("peer fsm event failed: %v", err)
		return
	}

	// Reschedule a new parent to children of peer to exclude the current failed peer
	blocklist := set.NewSafeSet()
	blocklist.Add(peer.ID)

	peer.Children.Range(func(_, value interface{}) bool {
		child, ok := value.(*resource.Peer)
		if !ok {
			return true
		}

		// Children state is PeerStateRunning will be rescheduled
		if !child.FSM.Is(resource.PeerStateRunning) {
			child.Log.Infof("peer can not be rescheduled because peer state is %s", peer.FSM.Current())
			return true
		}

		c.ScheduleParent(ctx, child, blocklist)
		return true
	})
}

func (c *callback) PeerLeave(ctx context.Context, peer *resource.Peer) {
	if err := peer.FSM.Event(resource.PeerEventLeave); err != nil {
		peer.Log.Errorf("peer fsm event failed: %v", err)
		return
	}

	peer.Children.Range(func(_, value interface{}) bool {
		child, ok := value.(*resource.Peer)
		if !ok {
			return true
		}

		// Children state is PeerStateRunning will be rescheduled
		if !child.FSM.Is(resource.PeerStateRunning) {
			child.Log.Infof("peer can not be rescheduled because peer state is %s", peer.FSM.Current())
			return true
		}

		// Reschedule a new parent to children of peer to exclude the current leave peer
		blocklist := set.NewSafeSet()
		blocklist.Add(peer.ID)

		c.ScheduleParent(ctx, child, blocklist)
		return true
	})

	peer.DeleteParent()
	c.resource.PeerManager().Delete(peer.ID)
}

// Conditions for the task to switch to the TaskStateSucceeded are:
// 1. CDN downloads the resource successfully
// 2. Dfdaemon back-to-source to download successfully
func (c *callback) TaskSuccess(ctx context.Context, peer *resource.Peer, task *resource.Task, endOfPiece *rpcscheduler.PeerResult) {
	if err := task.FSM.Event(resource.TaskEventDownloadSucceeded); err != nil {
		task.Log.Errorf("task fsm event failed: %v", err)
		return
	}

	// Update task's resource total piece count and content length
	task.TotalPieceCount.Store(endOfPiece.TotalPieceCount)
	task.ContentLength.Store(endOfPiece.ContentLength)
}

// Conditions for the task to switch to the TaskStateSucceeded are:
// 1. CDN downloads the resource falied
// 2. Dfdaemon back-to-source to download failed
func (c *callback) TaskFail(ctx context.Context, task *resource.Task) {
	if err := task.FSM.Event(resource.TaskEventDownloadFailed); err != nil {
		task.Log.Errorf("task fsm event failed: %v", err)
		return
	}
}
