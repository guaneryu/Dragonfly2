// Code generated by MockGen. DO NOT EDIT.
// Source: ../../../peer/piece_downloader.go

// Package mock_peer is a generated GoMock package.
package mock_peer

import (
	io "io"
	reflect "reflect"

	peer "d7y.io/dragonfly/v2/client/daemon/peer"
	gomock "github.com/golang/mock/gomock"
)

// MockPieceDownloader is a mock of PieceDownloader interface
type MockPieceDownloader struct {
	ctrl     *gomock.Controller
	recorder *MockPieceDownloaderMockRecorder
}

// MockPieceDownloaderMockRecorder is the mock recorder for MockPieceDownloader
type MockPieceDownloaderMockRecorder struct {
	mock *MockPieceDownloader
}

// NewMockPieceDownloader creates a new mock instance
func NewMockPieceDownloader(ctrl *gomock.Controller) *MockPieceDownloader {
	mock := &MockPieceDownloader{ctrl: ctrl}
	mock.recorder = &MockPieceDownloaderMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use
func (m *MockPieceDownloader) EXPECT() *MockPieceDownloaderMockRecorder {
	return m.recorder
}

// DownloadPiece mocks base method
func (m *MockPieceDownloader) DownloadPiece(arg0 *peer.DownloadPieceRequest) (io.ReadCloser, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DownloadPiece", arg0)
	ret0, _ := ret[0].(io.ReadCloser)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// DownloadPiece indicates an expected call of DownloadPiece
func (mr *MockPieceDownloaderMockRecorder) DownloadPiece(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DownloadPiece", reflect.TypeOf((*MockPieceDownloader)(nil).DownloadPiece), arg0)
}
