package contracts

import (
	"fmt"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/meshplus/bitxhub-core/boltvm/mock_stub"
	"github.com/meshplus/bitxhub-model/constant"
	"github.com/meshplus/bitxhub-model/pb"
	"github.com/stretchr/testify/assert"
)

func TestTransactionManager_BeginMultiTXs(t *testing.T) {
	id0 := "1356:chain0:service0-1356:chain1:service1-1"
	id1 := "1356:chain0:service0-1356:chain2:service2-1"
	globalId := "globalId"

	setup := func(t *testing.T) (*mock_stub.MockStub, *TransactionManager) {
		mockCtl := gomock.NewController(t)
		mockStub := mock_stub.NewMockStub(mockCtl)
		mockStub.EXPECT().GetCurrentHeight().Return(uint64(100)).AnyTimes()
		im := &TransactionManager{Stub: mockStub}
		return mockStub, im
	}

	// check current caller
	t.Run("case1", func(t *testing.T) {
		mockStub, im := setup(t)

		mockStub.EXPECT().CurrentCaller().Return(constant.TransactionMgrContractAddr.Address().String()).MaxTimes(2)
		res := im.BeginMultiTXs(globalId, id0, 10, false, 2)
		assert.False(t, res.Ok)
		assert.Contains(t, string(res.Result), "current caller 0x000000000000000000000000000000000000000F is not allowed")
	})

	// add tx
	t.Run("case2", func(t *testing.T) {
		mockStub, im := setup(t)
		mockStub.EXPECT().CurrentCaller().Return(constant.InterchainContractAddr.Address().String()).MaxTimes(1)

		mockStub.EXPECT().Get(GlobalTxInfoKey(globalId)).Return(false, nil).MaxTimes(1)
		mockStub.EXPECT().Get(TimeoutKey(uint64(110))).Return(false, nil).MaxTimes(1)
		mockStub.EXPECT().Set(TimeoutKey(uint64(110)), []byte(globalId)).MaxTimes(1)

		mockStub.EXPECT().Set(id0, []byte(globalId)).MaxTimes(1)
		txInfo := pb.TransactionInfo{
			GlobalState:  pb.TransactionStatus_BEGIN,
			ChildTxInfo:  map[string]pb.TransactionStatus{id0: pb.TransactionStatus_BEGIN},
			Height:       110,
			ChildTxCount: 2,
		}
		data, err := txInfo.Marshal()
		assert.Nil(t, err)
		mockStub.EXPECT().Add(GlobalTxInfoKey(globalId), data).MaxTimes(1)

		res := im.BeginMultiTXs(globalId, id0, 10, false, 2)
		assert.True(t, res.Ok)
		statusChange := pb.StatusChange{}
		err = statusChange.Unmarshal(res.Result)
		assert.Nil(t, err)
		assert.Equal(t, pb.TransactionStatus(-1), statusChange.PrevStatus)
		assert.Equal(t, pb.TransactionStatus_BEGIN, statusChange.CurStatus)
		assert.Equal(t, 0, len(statusChange.OtherIBTPIDs))
	})

	// add failed tx
	t.Run("case3", func(t *testing.T) {
		mockStub, im := setup(t)
		mockStub.EXPECT().CurrentCaller().Return(constant.InterchainContractAddr.Address().String()).MaxTimes(1)

		txInfo := pb.TransactionInfo{
			GlobalState:  pb.TransactionStatus_BEGIN_FAILURE,
			ChildTxInfo:  map[string]pb.TransactionStatus{id0: pb.TransactionStatus_BEGIN_FAILURE},
			Height:       110,
			ChildTxCount: 2,
		}
		txInfoData, err := txInfo.Marshal()
		assert.Nil(t, err)
		mockStub.EXPECT().Get(GlobalTxInfoKey(globalId)).Return(false, nil).MaxTimes(1)
		mockStub.EXPECT().Set(id0, []byte(globalId)).MaxTimes(1)
		mockStub.EXPECT().Add(GlobalTxInfoKey(globalId), txInfoData).MaxTimes(1)
		res := im.BeginMultiTXs(globalId, id0, 10, true, 2)
		assert.True(t, res.Ok)
		statusChange := pb.StatusChange{}
		err = statusChange.Unmarshal(res.Result)
		assert.Nil(t, err)
		assert.Equal(t, pb.TransactionStatus(-1), statusChange.PrevStatus)
		assert.Equal(t, pb.TransactionStatus_BEGIN_FAILURE, statusChange.CurStatus)
		assert.Equal(t, 0, len(statusChange.OtherIBTPIDs))
	})

	// add existing child tx
	t.Run("case4", func(t *testing.T) {
		mockStub, im := setup(t)
		mockStub.EXPECT().CurrentCaller().Return(constant.InterchainContractAddr.Address().String()).MaxTimes(1)

		txInfo := pb.TransactionInfo{
			GlobalState:  pb.TransactionStatus_BEGIN_FAILURE,
			ChildTxInfo:  map[string]pb.TransactionStatus{id0: pb.TransactionStatus_BEGIN_FAILURE},
			Height:       110,
			ChildTxCount: 2,
		}
		txInfoData, err := txInfo.Marshal()
		assert.Nil(t, err)
		mockStub.EXPECT().Get(GlobalTxInfoKey(globalId)).Return(true, txInfoData).MaxTimes(1)
		res := im.BeginMultiTXs(globalId, id0, 10, false, 2)
		assert.False(t, res.Ok)
		assert.Contains(t, string(res.Result), fmt.Sprintf("child tx %s of global tx %s exists", id0, globalId))
	})

	// add child tx when GlobalState is BEGIN_FAILURE
	t.Run("case5", func(t *testing.T) {
		mockStub, im := setup(t)
		mockStub.EXPECT().CurrentCaller().Return(constant.InterchainContractAddr.Address().String()).MaxTimes(1)

		txInfo := pb.TransactionInfo{
			GlobalState:  pb.TransactionStatus_BEGIN_FAILURE,
			ChildTxInfo:  map[string]pb.TransactionStatus{id0: pb.TransactionStatus_BEGIN_FAILURE},
			Height:       110,
			ChildTxCount: 2,
		}
		txInfoData, err := txInfo.Marshal()
		assert.Nil(t, err)
		mockStub.EXPECT().Get(GlobalTxInfoKey(globalId)).Return(true, txInfoData).MaxTimes(1)
		mockStub.EXPECT().Set(GlobalTxInfoKey(globalId), gomock.Any()).Do(func(k, v interface{}) {
			gotTxInfo := pb.TransactionInfo{}
			err := gotTxInfo.Unmarshal(v.([]byte))
			assert.Nil(t, err)
			assert.Equal(t, pb.TransactionInfo{
				GlobalState:  pb.TransactionStatus_BEGIN_FAILURE,
				ChildTxInfo:  map[string]pb.TransactionStatus{id0: pb.TransactionStatus_BEGIN_FAILURE, id1: pb.TransactionStatus_BEGIN_FAILURE},
				Height:       110,
				ChildTxCount: 2,
			}, gotTxInfo)
		}).MaxTimes(1)
		mockStub.EXPECT().Set(id1, []byte(globalId)).MaxTimes(1)
		res := im.BeginMultiTXs(globalId, id1, 10, false, 2)
		assert.True(t, res.Ok)
		statusChange := pb.StatusChange{}
		err = statusChange.Unmarshal(res.Result)
		assert.Nil(t, err)
		assert.Equal(t, pb.TransactionStatus(-1), statusChange.PrevStatus)
		assert.Equal(t, pb.TransactionStatus_BEGIN_FAILURE, statusChange.CurStatus)
		assert.Equal(t, 0, len(statusChange.OtherIBTPIDs))
	})

	// add child tx when GlobalState is BEGIN
	t.Run("case6", func(t *testing.T) {
		mockStub, im := setup(t)
		mockStub.EXPECT().CurrentCaller().Return(constant.InterchainContractAddr.Address().String()).MaxTimes(1)

		txInfo := pb.TransactionInfo{
			GlobalState:  pb.TransactionStatus_BEGIN,
			ChildTxInfo:  map[string]pb.TransactionStatus{id0: pb.TransactionStatus_BEGIN},
			Height:       110,
			ChildTxCount: 2,
		}
		txInfoData, err := txInfo.Marshal()
		assert.Nil(t, err)
		mockStub.EXPECT().Get(GlobalTxInfoKey(globalId)).Return(true, txInfoData).MaxTimes(1)
		mockStub.EXPECT().Set(GlobalTxInfoKey(globalId), gomock.Any()).Do(func(k, v interface{}) {
			gotTxInfo := pb.TransactionInfo{}
			err := gotTxInfo.Unmarshal(v.([]byte))
			assert.Nil(t, err)
			assert.Equal(t, pb.TransactionInfo{
				GlobalState:  pb.TransactionStatus_BEGIN,
				ChildTxInfo:  map[string]pb.TransactionStatus{id0: pb.TransactionStatus_BEGIN, id1: pb.TransactionStatus_BEGIN},
				Height:       110,
				ChildTxCount: 2,
			}, gotTxInfo)
		}).MaxTimes(1)
		mockStub.EXPECT().Set(id1, []byte(globalId)).MaxTimes(1)
		res := im.BeginMultiTXs(globalId, id1, 10, false, 2)
		assert.True(t, res.Ok)
		statusChange := pb.StatusChange{}
		err = statusChange.Unmarshal(res.Result)
		assert.Nil(t, err)
		assert.Equal(t, pb.TransactionStatus(-1), statusChange.PrevStatus)
		assert.Equal(t, pb.TransactionStatus_BEGIN, statusChange.CurStatus)
		assert.Equal(t, 0, len(statusChange.OtherIBTPIDs))
	})

	// add failed child tx when GlobalState is BEGIN
	t.Run("case7", func(t *testing.T) {
		mockStub, im := setup(t)
		mockStub.EXPECT().CurrentCaller().Return(constant.InterchainContractAddr.Address().String()).MaxTimes(1)

		txInfo := pb.TransactionInfo{
			GlobalState:  pb.TransactionStatus_BEGIN,
			ChildTxInfo:  map[string]pb.TransactionStatus{id0: pb.TransactionStatus_BEGIN},
			Height:       110,
			ChildTxCount: 2,
		}
		txInfoData, err := txInfo.Marshal()
		assert.Nil(t, err)
		mockStub.EXPECT().Get(GlobalTxInfoKey(globalId)).Return(true, txInfoData).MaxTimes(1)
		mockStub.EXPECT().Set(GlobalTxInfoKey(globalId), gomock.Any()).Do(func(k, v interface{}) {
			gotTxInfo := pb.TransactionInfo{}
			err := gotTxInfo.Unmarshal(v.([]byte))
			assert.Nil(t, err)
			assert.Equal(t, pb.TransactionInfo{
				GlobalState:  pb.TransactionStatus_BEGIN_FAILURE,
				ChildTxInfo:  map[string]pb.TransactionStatus{id0: pb.TransactionStatus_BEGIN_FAILURE, id1: pb.TransactionStatus_BEGIN_FAILURE},
				Height:       110,
				ChildTxCount: 2,
			}, gotTxInfo)
		}).MaxTimes(1)
		mockStub.EXPECT().Get(TimeoutKey(uint64(110))).Return(false, nil).MaxTimes(1)
		mockStub.EXPECT().Set(TimeoutKey(uint64(110)), []byte(globalId)).MaxTimes(1)
		mockStub.EXPECT().Set(id1, []byte(globalId)).MaxTimes(1)
		res := im.BeginMultiTXs(globalId, id1, 10, true, 2)
		assert.True(t, res.Ok)
		statusChange := pb.StatusChange{}
		err = statusChange.Unmarshal(res.Result)
		assert.Nil(t, err)
		assert.Equal(t, pb.TransactionStatus(-1), statusChange.PrevStatus)
		assert.Equal(t, pb.TransactionStatus_BEGIN_FAILURE, statusChange.CurStatus)
		assert.Equal(t, 1, len(statusChange.OtherIBTPIDs))
		assert.Equal(t, id0, statusChange.OtherIBTPIDs[0])
	})

	// add failed child tx when GlobalState is BEGIN_FAILURE
	t.Run("case8", func(t *testing.T) {
		mockStub, im := setup(t)
		mockStub.EXPECT().CurrentCaller().Return(constant.InterchainContractAddr.Address().String()).MaxTimes(1)

		txInfo := pb.TransactionInfo{
			GlobalState:  pb.TransactionStatus_BEGIN_FAILURE,
			ChildTxInfo:  map[string]pb.TransactionStatus{id0: pb.TransactionStatus_BEGIN_FAILURE},
			Height:       110,
			ChildTxCount: 2,
		}
		txInfoData, err := txInfo.Marshal()
		assert.Nil(t, err)
		mockStub.EXPECT().Get(GlobalTxInfoKey(globalId)).Return(true, txInfoData).MaxTimes(1)
		mockStub.EXPECT().Set(GlobalTxInfoKey(globalId), gomock.Any()).Do(func(k, v interface{}) {
			gotTxInfo := pb.TransactionInfo{}
			err := gotTxInfo.Unmarshal(v.([]byte))
			assert.Nil(t, err)
			assert.Equal(t, pb.TransactionInfo{
				GlobalState:  pb.TransactionStatus_BEGIN_FAILURE,
				ChildTxInfo:  map[string]pb.TransactionStatus{id0: pb.TransactionStatus_BEGIN_FAILURE, id1: pb.TransactionStatus_BEGIN_FAILURE},
				Height:       110,
				ChildTxCount: 2,
			}, gotTxInfo)
		}).MaxTimes(1)
		mockStub.EXPECT().Set(id1, []byte(globalId)).MaxTimes(1)
		res := im.BeginMultiTXs(globalId, id1, 10, true, 2)
		assert.True(t, res.Ok)
		statusChange := pb.StatusChange{}
		err = statusChange.Unmarshal(res.Result)
		assert.Nil(t, err)
		assert.Equal(t, pb.TransactionStatus(-1), statusChange.PrevStatus)
		assert.Equal(t, pb.TransactionStatus_BEGIN_FAILURE, statusChange.CurStatus)
		assert.Equal(t, 0, len(statusChange.OtherIBTPIDs))
	})

	// add child tx when GlobalState is BEGIN_ROLLBACK
	t.Run("case9", func(t *testing.T) {
		mockStub, im := setup(t)
		mockStub.EXPECT().CurrentCaller().Return(constant.InterchainContractAddr.Address().String()).MaxTimes(1)

		txInfo := pb.TransactionInfo{
			GlobalState:  pb.TransactionStatus_BEGIN_ROLLBACK,
			ChildTxInfo:  map[string]pb.TransactionStatus{id0: pb.TransactionStatus_BEGIN_ROLLBACK},
			Height:       110,
			ChildTxCount: 2,
		}
		txInfoData, err := txInfo.Marshal()
		assert.Nil(t, err)
		mockStub.EXPECT().Get(GlobalTxInfoKey(globalId)).Return(true, txInfoData).MaxTimes(1)
		mockStub.EXPECT().Set(GlobalTxInfoKey(globalId), gomock.Any()).Do(func(k, v interface{}) {
			gotTxInfo := pb.TransactionInfo{}
			err := gotTxInfo.Unmarshal(v.([]byte))
			assert.Nil(t, err)
			assert.Equal(t, pb.TransactionInfo{
				GlobalState:  pb.TransactionStatus_BEGIN_ROLLBACK,
				ChildTxInfo:  map[string]pb.TransactionStatus{id0: pb.TransactionStatus_BEGIN_ROLLBACK, id1: pb.TransactionStatus_BEGIN_ROLLBACK},
				Height:       110,
				ChildTxCount: 2,
			}, gotTxInfo)
		}).MaxTimes(1)
		mockStub.EXPECT().Set(id1, []byte(globalId)).MaxTimes(1)
		res := im.BeginMultiTXs(globalId, id1, 10, false, 2)
		assert.True(t, res.Ok)
		statusChange := pb.StatusChange{}
		err = statusChange.Unmarshal(res.Result)
		assert.Nil(t, err)
		assert.Equal(t, pb.TransactionStatus(-1), statusChange.PrevStatus)
		assert.Equal(t, pb.TransactionStatus_BEGIN_ROLLBACK, statusChange.CurStatus)
		assert.Equal(t, 0, len(statusChange.OtherIBTPIDs))
	})
}

func TestTransactionManager_Begin(t *testing.T) {
	mockCtl := gomock.NewController(t)
	mockStub := mock_stub.NewMockStub(mockCtl)

	id := "1356:chain0:service0-1356:chain1:service1-1"
	mockStub.EXPECT().GetCurrentHeight().Return(uint64(100)).AnyTimes()
	mockStub.EXPECT().Set(gomock.Any(), gomock.Any()).AnyTimes()
	mockStub.EXPECT().Add(gomock.Any(), gomock.Any()).AnyTimes()
	im := &TransactionManager{Stub: mockStub}

	mockStub.EXPECT().CurrentCaller().Return(constant.TransactionMgrContractAddr.Address().String()).MaxTimes(2)
	res := im.Begin(id, 0, false)
	assert.False(t, res.Ok)

	mockStub.EXPECT().CurrentCaller().Return(constant.InterchainContractAddr.Address().String()).AnyTimes()
	res = im.Begin(id, 10, false)
	assert.True(t, res.Ok)
	statusChange := pb.StatusChange{}
	err := statusChange.Unmarshal(res.Result)
	assert.Nil(t, err)
	assert.Equal(t, pb.TransactionStatus(-1), statusChange.PrevStatus)
	assert.Equal(t, pb.TransactionStatus_BEGIN, statusChange.CurStatus)
	assert.Equal(t, 0, len(statusChange.OtherIBTPIDs))

	res = im.Begin(id, 10, true)
	assert.True(t, res.Ok)
	statusChange = pb.StatusChange{}
	err = statusChange.Unmarshal(res.Result)
	assert.Nil(t, err)
	assert.Equal(t, pb.TransactionStatus(-1), statusChange.PrevStatus)
	assert.Equal(t, pb.TransactionStatus_BEGIN_FAILURE, statusChange.CurStatus)
	assert.Equal(t, 0, len(statusChange.OtherIBTPIDs))
}

func TestTransactionManager_Report(t *testing.T) {
	mockCtl := gomock.NewController(t)
	mockStub := mock_stub.NewMockStub(mockCtl)

	id := "1356:chain0:service0-1356:chain1:service1-1"
	recBegin := pb.TransactionRecord{
		Height: 100,
		Status: pb.TransactionStatus_BEGIN,
	}
	recBeginData, err := recBegin.Marshal()
	assert.Nil(t, err)
	recSuccess := pb.TransactionRecord{
		Height: 100,
		Status: pb.TransactionStatus_SUCCESS,
	}
	recSuccessData, err := recSuccess.Marshal()
	assert.Nil(t, err)
	recFailure := pb.TransactionRecord{
		Height: 100,
		Status: pb.TransactionStatus_FAILURE,
	}
	recFailureData, err := recFailure.Marshal()
	assert.Nil(t, err)

	im := &TransactionManager{Stub: mockStub}

	mockStub.EXPECT().CurrentCaller().Return(constant.TransactionMgrContractAddr.Address().String()).MaxTimes(2)
	res := im.Report(id, 0)
	assert.False(t, res.Ok)
	assert.Contains(t, string(res.Result), "current caller 0x000000000000000000000000000000000000000F is not allowed")

	mockStub.EXPECT().CurrentCaller().Return(constant.InterchainContractAddr.Address().String()).AnyTimes()

	mockStub.EXPECT().Get(TxInfoKey(id)).Return(true, recSuccessData).MaxTimes(1)
	res = im.Report(id, 0)
	assert.False(t, res.Ok)
	assert.Contains(t, string(res.Result), fmt.Sprintf("transaction %s with state %v get unexpected receipt %v", id, recSuccess.Status, 0))

	mockStub.EXPECT().Get(TxInfoKey(id)).Return(true, recBeginData).MaxTimes(1)
	mockStub.EXPECT().Set(TxInfoKey(id), recSuccessData).MaxTimes(1)
	res = im.Report(id, int32(pb.IBTP_RECEIPT_SUCCESS))
	assert.True(t, res.Ok)
	statusChange := pb.StatusChange{}
	err = statusChange.Unmarshal(res.Result)
	assert.Nil(t, err)
	assert.Equal(t, pb.TransactionStatus_BEGIN, statusChange.PrevStatus)
	assert.Equal(t, pb.TransactionStatus_SUCCESS, statusChange.CurStatus)
	assert.Equal(t, 0, len(statusChange.OtherIBTPIDs))

	mockStub.EXPECT().Get(TxInfoKey(id)).Return(true, recBeginData).MaxTimes(1)
	mockStub.EXPECT().Set(TxInfoKey(id), recFailureData).MaxTimes(1)
	res = im.Report(id, int32(pb.IBTP_RECEIPT_FAILURE))
	assert.True(t, res.Ok)
	statusChange = pb.StatusChange{}
	err = statusChange.Unmarshal(res.Result)
	assert.Nil(t, err)
	assert.Equal(t, pb.TransactionStatus_BEGIN, statusChange.PrevStatus)
	assert.Equal(t, pb.TransactionStatus_FAILURE, statusChange.CurStatus)
	assert.Equal(t, 0, len(statusChange.OtherIBTPIDs))

	id0 := "1356:chain0:service0"
	id1 := "1356:chain1:service1"
	id2 := "1356:chain2:service2"
	globalID := "globalID"

	mockStub.EXPECT().Get(TxInfoKey(id0)).Return(false, nil).AnyTimes()
	mockStub.EXPECT().Get(TxInfoKey(id2)).Return(false, nil).AnyTimes()
	mockStub.EXPECT().Get(id0).Return(false, nil)
	res = im.Report(id0, int32(pb.IBTP_RECEIPT_SUCCESS))
	assert.False(t, res.Ok)
	assert.Contains(t, string(res.Result), fmt.Sprintf("transaction id %s does not exist", id0))

	mockStub.EXPECT().Get(gomock.Not(GlobalTxInfoKey(globalID))).Return(true, []byte(globalID)).AnyTimes()
	mockStub.EXPECT().Get(GlobalTxInfoKey(globalID)).Return(false, nil).MaxTimes(1)
	res = im.Report(id0, int32(pb.IBTP_RECEIPT_SUCCESS))
	assert.False(t, res.Ok)
	assert.Contains(t, string(res.Result), fmt.Sprintf("global tx %s of child tx %s does not exist", globalID, id0))

	txInfo := pb.TransactionInfo{
		GlobalState:  pb.TransactionStatus_BEGIN,
		Height:       110,
		ChildTxInfo:  map[string]pb.TransactionStatus{id0: pb.TransactionStatus_SUCCESS, id1: pb.TransactionStatus_BEGIN},
		ChildTxCount: 2,
	}
	txInfoData, err := txInfo.Marshal()
	assert.Nil(t, err)
	mockStub.EXPECT().Get(GlobalTxInfoKey(globalID)).Return(true, txInfoData).MaxTimes(1)
	res = im.Report(id2, int32(pb.IBTP_RECEIPT_SUCCESS))
	assert.False(t, res.Ok)
	assert.Contains(t, string(res.Result), fmt.Sprintf("%s is not in transaction %s, %v", id2, globalID, txInfo))

	mockStub.EXPECT().Get(GlobalTxInfoKey(globalID)).Return(true, txInfoData).MaxTimes(1)
	res = im.Report(id0, int32(pb.IBTP_RECEIPT_SUCCESS))
	assert.False(t, res.Ok)
	assert.Contains(t, string(res.Result), fmt.Sprintf("child tx %s with state %v get unexpected receipt %v", id0, pb.TransactionStatus_SUCCESS, int32(pb.IBTP_RECEIPT_SUCCESS)))

	txInfo.GlobalState = pb.TransactionStatus_SUCCESS
	txInfo.ChildTxInfo[id0] = pb.TransactionStatus_BEGIN
	txInfo.ChildTxInfo[id1] = pb.TransactionStatus_SUCCESS
	txInfoData, err = txInfo.Marshal()
	assert.Nil(t, err)
	mockStub.EXPECT().Get(GlobalTxInfoKey(globalID)).Return(true, txInfoData).MaxTimes(1)
	res = im.Report(id0, int32(pb.IBTP_RECEIPT_SUCCESS))
	assert.False(t, res.Ok)
	assert.Contains(t, string(res.Result), fmt.Sprintf("global tx of child tx %s with state %v get unexpected receipt %v", id0, txInfo.GlobalState, int32(pb.IBTP_RECEIPT_SUCCESS)))

	txInfo = pb.TransactionInfo{
		GlobalState:  pb.TransactionStatus_BEGIN,
		Height:       txInfo.Height,
		ChildTxInfo:  map[string]pb.TransactionStatus{id0: pb.TransactionStatus_BEGIN, id1: pb.TransactionStatus_SUCCESS},
		ChildTxCount: txInfo.ChildTxCount,
	}
	txInfoData, err = txInfo.Marshal()
	assert.Nil(t, err)
	mockStub.EXPECT().Get(GlobalTxInfoKey(globalID)).Return(true, txInfoData).MaxTimes(1)
	mockStub.EXPECT().Set(GlobalTxInfoKey(globalID), gomock.Any()).Do(func(k, v interface{}) {
		gotTxInfo := pb.TransactionInfo{}
		err := gotTxInfo.Unmarshal(v.([]byte))
		assert.Nil(t, err)
		assert.Equal(t, pb.TransactionInfo{
			GlobalState:  pb.TransactionStatus_SUCCESS,
			Height:       txInfo.Height,
			ChildTxInfo:  map[string]pb.TransactionStatus{id0: pb.TransactionStatus_SUCCESS, id1: pb.TransactionStatus_SUCCESS},
			ChildTxCount: txInfo.ChildTxCount,
		}, gotTxInfo)
	}).MaxTimes(1)
	mockStub.EXPECT().Get(TimeoutKey(txInfo.Height)).Return(true, []byte(globalID)).MaxTimes(1)
	mockStub.EXPECT().Set(TimeoutKey(txInfo.Height), []byte{}).MaxTimes(1)
	res = im.Report(id0, int32(pb.IBTP_RECEIPT_SUCCESS))
	assert.True(t, res.Ok)
	statusChange = pb.StatusChange{}
	err = statusChange.Unmarshal(res.Result)
	assert.Nil(t, err)
	assert.Equal(t, pb.TransactionStatus_BEGIN, statusChange.PrevStatus)
	assert.Equal(t, pb.TransactionStatus_SUCCESS, statusChange.CurStatus)
	assert.Equal(t, 1, len(statusChange.OtherIBTPIDs))

	txInfo = pb.TransactionInfo{
		GlobalState:  pb.TransactionStatus_BEGIN,
		Height:       txInfo.Height,
		ChildTxInfo:  map[string]pb.TransactionStatus{id0: pb.TransactionStatus_BEGIN, id1: pb.TransactionStatus_BEGIN},
		ChildTxCount: txInfo.ChildTxCount,
	}
	txInfoData, err = txInfo.Marshal()
	assert.Nil(t, err)
	mockStub.EXPECT().Get(GlobalTxInfoKey(globalID)).Return(true, txInfoData).MaxTimes(1)
	mockStub.EXPECT().Set(GlobalTxInfoKey(globalID), gomock.Any()).Do(func(k, v interface{}) {
		gotTxInfo := pb.TransactionInfo{}
		err := gotTxInfo.Unmarshal(v.([]byte))
		assert.Nil(t, err)
		assert.Equal(t, pb.TransactionInfo{
			GlobalState:  pb.TransactionStatus_BEGIN,
			Height:       txInfo.Height,
			ChildTxInfo:  map[string]pb.TransactionStatus{id0: pb.TransactionStatus_SUCCESS, id1: pb.TransactionStatus_BEGIN},
			ChildTxCount: txInfo.ChildTxCount,
		}, gotTxInfo)
	}).MaxTimes(1)
	mockStub.EXPECT().Get(TimeoutKey(txInfo.Height)).Return(true, []byte(globalID)).MaxTimes(1)
	mockStub.EXPECT().Set(TimeoutKey(txInfo.Height), []byte{}).MaxTimes(1)
	res = im.Report(id0, int32(pb.IBTP_RECEIPT_SUCCESS))
	assert.True(t, res.Ok)
	statusChange = pb.StatusChange{}
	err = statusChange.Unmarshal(res.Result)
	assert.Nil(t, err)
	assert.Equal(t, pb.TransactionStatus_BEGIN, statusChange.PrevStatus)
	assert.Equal(t, pb.TransactionStatus_BEGIN, statusChange.CurStatus)
	assert.Equal(t, 1, len(statusChange.OtherIBTPIDs))

	txInfo = pb.TransactionInfo{
		GlobalState:  pb.TransactionStatus_BEGIN,
		Height:       txInfo.Height,
		ChildTxInfo:  map[string]pb.TransactionStatus{id0: pb.TransactionStatus_BEGIN, id1: pb.TransactionStatus_BEGIN},
		ChildTxCount: txInfo.ChildTxCount,
	}
	txInfoData, err = txInfo.Marshal()
	assert.Nil(t, err)
	mockStub.EXPECT().Get(GlobalTxInfoKey(globalID)).Return(true, txInfoData).MaxTimes(1)
	mockStub.EXPECT().Set(GlobalTxInfoKey(globalID), gomock.Any()).Do(func(k, v interface{}) {
		gotTxInfo := pb.TransactionInfo{}
		err := gotTxInfo.Unmarshal(v.([]byte))
		assert.Nil(t, err)
		assert.Equal(t, pb.TransactionInfo{
			GlobalState:  pb.TransactionStatus_BEGIN_FAILURE,
			Height:       txInfo.Height,
			ChildTxInfo:  map[string]pb.TransactionStatus{id0: pb.TransactionStatus_FAILURE, id1: pb.TransactionStatus_BEGIN_FAILURE},
			ChildTxCount: txInfo.ChildTxCount,
		}, gotTxInfo)
	}).MaxTimes(1)
	mockStub.EXPECT().Get(TimeoutKey(txInfo.Height)).Return(true, []byte(globalID)).MaxTimes(1)
	mockStub.EXPECT().Set(TimeoutKey(txInfo.Height), []byte{}).MaxTimes(1)
	res = im.Report(id0, int32(pb.IBTP_RECEIPT_FAILURE))
	assert.True(t, res.Ok)
	statusChange = pb.StatusChange{}
	err = statusChange.Unmarshal(res.Result)
	assert.Nil(t, err)
	assert.Equal(t, pb.TransactionStatus_BEGIN, statusChange.PrevStatus)
	assert.Equal(t, pb.TransactionStatus_BEGIN_FAILURE, statusChange.CurStatus)
	assert.Equal(t, 1, len(statusChange.OtherIBTPIDs))
	assert.Equal(t, id1, statusChange.OtherIBTPIDs[0])
}

func TestTransactionManager_GetStatus(t *testing.T) {
	mockCtl := gomock.NewController(t)
	mockStub := mock_stub.NewMockStub(mockCtl)

	id := "id"
	txInfoKey := fmt.Sprintf("%s-%s", PREFIX, id)
	globalInfoKey := fmt.Sprintf("global-%s-%s", PREFIX, id)

	recSuccess := pb.TransactionRecord{
		Height: 100,
		Status: pb.TransactionStatus_SUCCESS,
	}
	recSuccessData, err := recSuccess.Marshal()
	assert.Nil(t, err)

	im := &TransactionManager{Stub: mockStub}

	mockStub.EXPECT().Get(txInfoKey).Return(true, recSuccessData).MaxTimes(1)
	res := im.GetStatus(id)
	assert.True(t, res.Ok)
	assert.Equal(t, "3", string(res.Result))

	txInfo := pb.TransactionInfo{
		GlobalState: pb.TransactionStatus_BEGIN,
		ChildTxInfo: make(map[string]pb.TransactionStatus),
	}
	txInfoData, err := txInfo.Marshal()
	assert.Nil(t, err)
	mockStub.EXPECT().Get(txInfoKey).Return(false, nil).AnyTimes()
	mockStub.EXPECT().Get(globalInfoKey).Return(true, txInfoData).MaxTimes(1)
	res = im.GetStatus(id)
	assert.True(t, res.Ok)
	assert.Equal(t, "0", string(res.Result))

	mockStub.EXPECT().Get(globalInfoKey).Return(false, nil).AnyTimes()
	mockStub.EXPECT().Get(id).Return(false, nil).MaxTimes(1)
	res = im.GetStatus(id)
	assert.False(t, res.Ok)
	assert.Contains(t, string(res.Result), fmt.Sprintf("cannot get global id of child tx id %s", id))

	globalId := "globalId"
	globalIdInfoKey := fmt.Sprintf("global-%s-%s", PREFIX, globalId)
	mockStub.EXPECT().Get(id).Return(true, []byte(globalId)).AnyTimes()
	mockStub.EXPECT().Get(globalIdInfoKey).Return(false, nil).MaxTimes(1)
	res = im.GetStatus(id)
	assert.False(t, res.Ok)
	assert.Contains(t, string(res.Result), fmt.Sprintf("global tx %s of child tx %s does not exist", globalId, id))

	txInfoData, err = txInfo.Marshal()
	assert.Nil(t, err)
	mockStub.EXPECT().Get(globalIdInfoKey).Return(true, txInfoData).MaxTimes(1)
	res = im.GetStatus(id)
	assert.True(t, res.Ok)
	assert.Equal(t, "0", string(res.Result))
}
