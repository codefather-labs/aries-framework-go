/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package didexchange

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/btcsuite/btcutil/base58"
	"github.com/stretchr/testify/require"

	"github.com/hyperledger/aries-framework-go/pkg/didcomm/common/model"
	"github.com/hyperledger/aries-framework-go/pkg/didcomm/common/service"
	"github.com/hyperledger/aries-framework-go/pkg/didcomm/protocol/decorator"
	diddoc "github.com/hyperledger/aries-framework-go/pkg/doc/did"
	mockdispatcher "github.com/hyperledger/aries-framework-go/pkg/internal/mock/didcomm/dispatcher"
	"github.com/hyperledger/aries-framework-go/pkg/internal/mock/didcomm/protocol"
	mockdidresolver "github.com/hyperledger/aries-framework-go/pkg/internal/mock/didresolver"
	mockstorage "github.com/hyperledger/aries-framework-go/pkg/internal/mock/storage"
	mockdid "github.com/hyperledger/aries-framework-go/pkg/internal/mock/vdr/didcreator"
)

func TestNoopState(t *testing.T) {
	noop := &noOp{}
	require.Equal(t, "noop", noop.Name())

	t.Run("must not transition to any state", func(t *testing.T) {
		all := []state{&null{}, &invited{}, &requested{}, &responded{}, &completed{}}
		for _, s := range all {
			require.False(t, noop.CanTransitionTo(s))
		}
	})
}

// null state can transition to invited state or requested state
func TestNullState(t *testing.T) {
	null := &null{}
	require.Equal(t, "null", null.Name())
	require.False(t, null.CanTransitionTo(null))
	require.True(t, null.CanTransitionTo(&invited{}))
	require.True(t, null.CanTransitionTo(&requested{}))
	require.False(t, null.CanTransitionTo(&responded{}))
	require.False(t, null.CanTransitionTo(&completed{}))
}

// invited can only transition to requested state
func TestInvitedState(t *testing.T) {
	invited := &invited{}
	require.Equal(t, "invited", invited.Name())
	require.False(t, invited.CanTransitionTo(&null{}))
	require.False(t, invited.CanTransitionTo(invited))
	require.True(t, invited.CanTransitionTo(&requested{}))
	require.False(t, invited.CanTransitionTo(&responded{}))
	require.False(t, invited.CanTransitionTo(&completed{}))
}

// requested can only transition to responded state
func TestRequestedState(t *testing.T) {
	requested := &requested{}
	require.Equal(t, "requested", requested.Name())
	require.False(t, requested.CanTransitionTo(&null{}))
	require.False(t, requested.CanTransitionTo(&invited{}))
	require.False(t, requested.CanTransitionTo(requested))
	require.True(t, requested.CanTransitionTo(&responded{}))
	require.False(t, requested.CanTransitionTo(&completed{}))
}

// responded can only transition to completed state
func TestRespondedState(t *testing.T) {
	responded := &responded{}
	require.Equal(t, "responded", responded.Name())
	require.False(t, responded.CanTransitionTo(&null{}))
	require.False(t, responded.CanTransitionTo(&invited{}))
	require.False(t, responded.CanTransitionTo(&requested{}))
	require.False(t, responded.CanTransitionTo(responded))
	require.True(t, responded.CanTransitionTo(&completed{}))
}

// completed is an end state
func TestCompletedState(t *testing.T) {
	completed := &completed{}
	require.Equal(t, "completed", completed.Name())
	require.False(t, completed.CanTransitionTo(&null{}))
	require.False(t, completed.CanTransitionTo(&invited{}))
	require.False(t, completed.CanTransitionTo(&requested{}))
	require.False(t, completed.CanTransitionTo(&responded{}))
	require.False(t, completed.CanTransitionTo(&abandoned{}))
	require.False(t, completed.CanTransitionTo(completed))
}

func TestAbandonedState(t *testing.T) {
	abandoned := &abandoned{}
	require.Equal(t, stateNameAbandoned, abandoned.Name())
	require.False(t, abandoned.CanTransitionTo(&null{}))
	require.False(t, abandoned.CanTransitionTo(&invited{}))
	require.False(t, abandoned.CanTransitionTo(&requested{}))
	require.False(t, abandoned.CanTransitionTo(&responded{}))
	require.False(t, abandoned.CanTransitionTo(&completed{}))
	connRec, _, _, err := abandoned.ExecuteInbound(&stateMachineMsg{}, "", &context{})
	require.Error(t, err)
	require.Nil(t, connRec)
	require.Contains(t, err.Error(), "not implemented")
}

func TestStateFromMsgType(t *testing.T) {
	t.Run("invited", func(t *testing.T) {
		expected := &invited{}
		actual, err := stateFromMsgType(InvitationMsgType)
		require.NoError(t, err)
		require.Equal(t, expected.Name(), actual.Name())
	})
	t.Run("requested", func(t *testing.T) {
		expected := &requested{}
		actual, err := stateFromMsgType(RequestMsgType)
		require.NoError(t, err)
		require.Equal(t, expected.Name(), actual.Name())
	})
	t.Run("responded", func(t *testing.T) {
		expected := &responded{}
		actual, err := stateFromMsgType(ResponseMsgType)
		require.NoError(t, err)
		require.Equal(t, expected.Name(), actual.Name())
	})
	t.Run("completed", func(t *testing.T) {
		expected := &completed{}
		actual, err := stateFromMsgType(AckMsgType)
		require.NoError(t, err)
		require.Equal(t, expected.Name(), actual.Name())
	})
	t.Run("invalid", func(t *testing.T) {
		actual, err := stateFromMsgType("invalid")
		require.Nil(t, actual)
		require.Error(t, err)
		require.Equal(t, "unrecognized msgType: invalid", err.Error())
	})
}

func TestStateFromName(t *testing.T) {
	t.Run("noop", func(t *testing.T) {
		expected := &noOp{}
		actual, err := stateFromName(expected.Name())
		require.NoError(t, err)
		require.Equal(t, expected.Name(), actual.Name())
	})
	t.Run("null", func(t *testing.T) {
		expected := &null{}
		actual, err := stateFromName(expected.Name())
		require.NoError(t, err)
		require.Equal(t, expected.Name(), actual.Name())
	})
	t.Run("invited", func(t *testing.T) {
		expected := &invited{}
		actual, err := stateFromName(expected.Name())
		require.NoError(t, err)
		require.Equal(t, expected.Name(), actual.Name())
	})
	t.Run("requested", func(t *testing.T) {
		expected := &requested{}
		actual, err := stateFromName(expected.Name())
		require.NoError(t, err)
		require.Equal(t, expected.Name(), actual.Name())
	})
	t.Run("responded", func(t *testing.T) {
		expected := &responded{}
		actual, err := stateFromName(expected.Name())
		require.NoError(t, err)
		require.Equal(t, expected.Name(), actual.Name())
	})
	t.Run("completed", func(t *testing.T) {
		expected := &completed{}
		actual, err := stateFromName(expected.Name())
		require.NoError(t, err)
		require.Equal(t, expected.Name(), actual.Name())
	})
	t.Run("abandoned", func(t *testing.T) {
		expected := &abandoned{}
		actual, err := stateFromName(expected.Name())
		require.NoError(t, err)
		require.Equal(t, expected.Name(), actual.Name())
	})
	t.Run("undefined", func(t *testing.T) {
		actual, err := stateFromName("undefined")
		require.Nil(t, actual)
		require.Error(t, err)
	})
}

// noOp.ExecuteInbound() returns nil, error
func TestNoOpState_Execute(t *testing.T) {
	_, followup, _, err := (&noOp{}).ExecuteInbound(&stateMachineMsg{}, "", &context{})
	require.Error(t, err)
	require.Nil(t, followup)
}

// null.ExecuteInbound() is a no-op
func TestNullState_Execute(t *testing.T) {
	_, followup, _, err := (&null{}).ExecuteInbound(&stateMachineMsg{}, "", &context{})
	require.NoError(t, err)
	require.IsType(t, &noOp{}, followup)
}

func TestInvitedState_Execute(t *testing.T) {
	t.Run("rejects msgs other than invitations", func(t *testing.T) {
		others := []string{RequestMsgType, ResponseMsgType, AckMsgType}
		for _, o := range others {
			_, _, _, err := (&invited{}).ExecuteInbound(&stateMachineMsg{header: &service.Header{Type: o}}, "", &context{})
			require.Error(t, err)
		}
	})
	t.Run("followup to 'requested' on inbound invitations", func(t *testing.T) {
		invitationPayloadBytes, err := json.Marshal(
			&Invitation{
				Type:            InvitationMsgType,
				ID:              randomString(),
				Label:           "Bob",
				RecipientKeys:   []string{"8HH5gYEeNc3z7PYXmd54d4x6qAfCNrqQqEB3nS7Zfu7K"},
				ServiceEndpoint: "https://localhost:8090",
				RoutingKeys:     []string{"8HH5gYEeNc3z7PYXmd54d4x6qAfCNrqQqEB3nS7Zfu7K"},
			},
		)
		require.NoError(t, err)
		connRec, followup, _, err := (&invited{}).ExecuteInbound(
			&stateMachineMsg{
				header:     &service.Header{Type: InvitationMsgType},
				payload:    invitationPayloadBytes,
				connRecord: &ConnectionRecord{},
			},
			"",
			&context{})
		require.NoError(t, err)
		require.Equal(t, &requested{}, followup)
		require.NotNil(t, connRec)
	})
}

func TestRequestedState_Execute(t *testing.T) {
	prov := protocol.MockProvider{}
	expected := &requested{}
	connRec, err := json.Marshal(&ConnectionRecord{State: expected.Name()})
	require.NoError(t, err)
	ctx := &context{outboundDispatcher: prov.OutboundDispatcher(),
		didCreator: &mockdid.MockDIDCreator{Doc: getMockDID()},
		signer:     &mockSigner{},
		connectionStore: NewConnectionRecorder(&mockStore{
			get: func(string) ([]byte, error) { return connRec, nil },
		}, nil), didStore: prov.DIDStore()}
	t.Run("rejects msgs other than invitations or requests", func(t *testing.T) {
		others := []string{ResponseMsgType, AckMsgType}
		for _, o := range others {
			_, _, _, e := (&requested{}).ExecuteInbound(&stateMachineMsg{header: &service.Header{Type: o}}, "", &context{})
			require.Error(t, e)
		}
	})
	// Alice receives an invitation from Bob
	invitationPayloadBytes, err := json.Marshal(
		&Invitation{
			Type:            InvitationMsgType,
			ID:              randomString(),
			Label:           "Bob",
			RecipientKeys:   []string{"8HH5gYEeNc3z7PYXmd54d4x6qAfCNrqQqEB3nS7Zfu7K"},
			ServiceEndpoint: "https://localhost:8090",
			RoutingKeys:     []string{"8HH5gYEeNc3z7PYXmd54d4x6qAfCNrqQqEB3nS7Zfu7K"},
		},
	)
	require.NoError(t, err)
	t.Run("hanlde inbound invitations", func(t *testing.T) {
		// nolint: govet
		msg, err := service.NewDIDCommMsg(invitationPayloadBytes)
		require.NoError(t, err)
		// nolint: govet
		thid, err := threadID(msg)
		require.NoError(t, err)
		connRec, _, _, e := (expected).ExecuteInbound(&stateMachineMsg{
			header:     msg.Header,
			payload:    msg.Payload,
			connRecord: &ConnectionRecord{},
		}, thid, ctx)
		require.NoError(t, e)
		require.NotNil(t, connRec.MyDID)
	})
	t.Run("inbound request error", func(t *testing.T) {
		_, followup, _, err := (&requested{}).ExecuteInbound(&stateMachineMsg{
			header:  &service.Header{Type: InvitationMsgType},
			payload: nil,
		}, "", &context{})
		require.Error(t, err)
		require.Nil(t, followup)
	})
	t.Run("create DID error", func(t *testing.T) {
		ctx2 := &context{outboundDispatcher: prov.OutboundDispatcher(),
			didCreator: &mockdid.MockDIDCreator{Failure: fmt.Errorf("create DID error")}}
		didDoc, err := ctx2.didCreator.Create(testMethod)
		require.Error(t, err)
		require.Nil(t, didDoc)
	})
	t.Run("handle inbound invitation public key error", func(t *testing.T) {
		connRec := &ConnectionRecord{State: (&requested{}).Name(), ThreadID: "test", ConnectionID: "123"}
		ctx2 := &context{outboundDispatcher: prov.OutboundDispatcher(),
			didCreator: &mockdid.MockDIDCreator{Doc: getMockDIDPublicKey()},
			signer:     &mockSigner{}, didStore: prov.DIDStore()}
		_, followup, _, err := (&requested{}).ExecuteInbound(&stateMachineMsg{
			header:     &service.Header{Type: InvitationMsgType},
			payload:    invitationPayloadBytes,
			connRecord: connRec,
		}, "", ctx2)
		require.Error(t, err)
		require.Nil(t, followup)
	})
}

func TestRespondedState_Execute(t *testing.T) {
	store := &mockstorage.MockStore{Store: make(map[string][]byte)}
	prov := protocol.MockProvider{}
	pubKey, privKey := generateKeyPair()
	ctx := &context{outboundDispatcher: prov.OutboundDispatcher(),
		didCreator:      &mockdid.MockDIDCreator{Doc: createDIDDocWithKey(pubKey)},
		signer:          &mockSigner{privateKey: privKey},
		connectionStore: NewConnectionRecorder(store, nil),
		didStore:        prov.DIDStore(),
	}
	newDidDoc, err := ctx.didCreator.Create(testMethod)
	require.NoError(t, err)
	t.Run("rejects msgs other than requests and responses", func(t *testing.T) {
		others := []string{InvitationMsgType, AckMsgType}
		for _, o := range others {
			_, _, _, e := (&responded{}).ExecuteInbound(&stateMachineMsg{header: &service.Header{Type: o}}, "", &context{})
			require.Error(t, e)
		}
	})
	// Prepare did-exchange inbound request
	request := &Request{
		Type:  RequestMsgType,
		ID:    randomString(),
		Label: "Bob",
		Connection: &Connection{
			DID:    newDidDoc.ID,
			DIDDoc: newDidDoc,
		},
	}
	requestPayloadBytes, err := json.Marshal(request)
	require.NoError(t, err)

	// Prepare did-exchange outbound response
	connection := &Connection{
		DID:    newDidDoc.ID,
		DIDDoc: newDidDoc,
	}
	connectionSignature, err := ctx.prepareConnectionSignature(connection)
	require.NoError(t, err)

	response := &Response{
		Type: ResponseMsgType,
		ID:   randomString(),
		Thread: &decorator.Thread{
			ID: request.ID,
		},
		ConnectionSignature: connectionSignature,
	}
	// Prepare did-exchange inbound response
	responsePayloadBytes, err := json.Marshal(response)
	require.NoError(t, err)
	t.Run("no followup for inbound requests", func(t *testing.T) {
		connRec := &ConnectionRecord{State: (&requested{}).Name(), ThreadID: request.ID, ConnectionID: "123"}
		err = ctx.connectionStore.saveConnectionRecord(connRec)
		require.NoError(t, err)
		err = ctx.connectionStore.saveNSThreadID(request.ID, findNameSpace(RequestMsgType), connRec.ConnectionID)
		require.NoError(t, err)
		connRec, followup, _, e := (&responded{}).ExecuteInbound(&stateMachineMsg{
			header:     &service.Header{Type: RequestMsgType},
			payload:    requestPayloadBytes,
			connRecord: &ConnectionRecord{},
		}, "", ctx)
		require.NoError(t, e)
		require.NotNil(t, connRec)
		require.IsType(t, &noOp{}, followup)
	})
	t.Run("followup to 'completed' on inbound responses", func(t *testing.T) {
		connRec := &ConnectionRecord{State: (&responded{}).Name(), ThreadID: request.ID, ConnectionID: "123"}
		err = ctx.connectionStore.saveConnectionRecord(connRec)
		require.NoError(t, err)
		err = ctx.connectionStore.saveNSThreadID(request.ID, findNameSpace(ResponseMsgType), connRec.ConnectionID)
		require.NoError(t, err)
		connRec, followup, _, e := (&responded{}).ExecuteInbound(
			&stateMachineMsg{
				header:     &service.Header{Type: ResponseMsgType},
				payload:    responsePayloadBytes,
				connRecord: connRec,
			}, "", ctx)
		require.NoError(t, e)
		require.NotNil(t, connRec)
		require.Equal(t, (&completed{}).Name(), followup.Name())
	})
	t.Run("handle inbound request public key error", func(t *testing.T) {
		ctx2 := &context{outboundDispatcher: prov.OutboundDispatcher(),
			didCreator: &mockdid.MockDIDCreator{Doc: getMockDIDPublicKey()}, signer: &mockSigner{},
			didStore: prov.DIDStore(), connectionStore: NewConnectionRecorder(store, nil)}
		_, followup, _, err := (&responded{}).ExecuteInbound(&stateMachineMsg{
			header:     &service.Header{Type: RequestMsgType},
			payload:    requestPayloadBytes,
			connRecord: &ConnectionRecord{},
		}, "", ctx2)
		require.Error(t, err)
		require.Nil(t, followup)
	})
}
func TestAbandonedState_Execute(t *testing.T) {
	t.Run("execute abandon state", func(t *testing.T) {
		connRec, s, action, err := (&abandoned{}).ExecuteInbound(&stateMachineMsg{
			header: &service.Header{Type: ResponseMsgType}}, "", &context{})
		require.Contains(t, err.Error(), "not implemented")
		require.Nil(t, connRec)
		require.Nil(t, s)
		require.Nil(t, action)
	})
}

// completed is an end state
func TestCompletedState_Execute(t *testing.T) {
	store := &mockstorage.MockStore{Store: make(map[string][]byte)}
	prov := protocol.MockProvider{}
	pubKey, privKey := generateKeyPair()
	ctx := &context{outboundDispatcher: prov.OutboundDispatcher(),
		didCreator: &mockdid.MockDIDCreator{Doc: createDIDDocWithKey(pubKey)},
		signer:     &mockSigner{privateKey: privKey}, connectionStore: NewConnectionRecorder(store, nil),
		didStore: prov.DIDStore()}
	newDidDoc, err := ctx.didCreator.Create(testMethod)
	require.NoError(t, err)
	outboundDestination := &service.Destination{RecipientKeys: []string{"test", "test2"}, ServiceEndpoint: "xyz"}
	t.Run("rejects msgs other than responses and acks", func(t *testing.T) {
		others := []string{InvitationMsgType, RequestMsgType}
		for _, o := range others {
			_, _, _, err = (&completed{}).ExecuteInbound(&stateMachineMsg{header: &service.Header{Type: o}}, "", &context{})
			require.Error(t, err)
		}
	})
	ackPayloadBytes, err := json.Marshal(&model.Ack{
		Type:   AckMsgType,
		ID:     randomString(),
		Status: ackStatusOK,
		Thread: &decorator.Thread{
			ID: "responseID",
		},
	},
	)
	require.NoError(t, err)
	connection := &Connection{
		DID:    newDidDoc.ID,
		DIDDoc: newDidDoc,
	}
	connectionSignature, err := ctx.prepareConnectionSignature(connection)
	require.NoError(t, err)
	response := &Response{
		Type: RequestMsgType,
		ID:   randomString(),
		Thread: &decorator.Thread{
			ID: generateRandomID(),
		},
		ConnectionSignature: connectionSignature,
	}
	responsePayloadBytes, err := json.Marshal(response)
	t.Run("no followup for inbound responses", func(t *testing.T) {
		connRec := &ConnectionRecord{State: (&responded{}).Name(), ThreadID: response.Thread.ID,
			ConnectionID: "123", MyDID: "did:peer:123456789abcdefghi#inbox", Namespace: myNSPrefix}
		err := ctx.connectionStore.saveNewConnectionRecord(connRec)
		require.NoError(t, err)
		ctx.didResolver = &mockdidresolver.MockResolver{Doc: getMockDID()}
		require.NoError(t, err)
		_, followup, _, e := (&completed{}).ExecuteInbound(&stateMachineMsg{
			header:              &service.Header{Type: ResponseMsgType},
			payload:             responsePayloadBytes,
			outboundDestination: outboundDestination,
			connRecord:          connRec,
		}, "", ctx)
		require.NoError(t, e)
		require.IsType(t, &noOp{}, followup)
	})
	t.Run("inbound responses missing signature data", func(t *testing.T) {
		response = &Response{
			Type: RequestMsgType,
			ID:   randomString(),
			Thread: &decorator.Thread{
				ID: "responseID",
			},
			ConnectionSignature: &ConnectionSignature{},
		}
		respPayloadBytes, err := json.Marshal(response)
		require.NoError(t, err)
		_, followup, _, err := (&completed{}).ExecuteInbound(&stateMachineMsg{
			header:              &service.Header{Type: ResponseMsgType},
			payload:             respPayloadBytes,
			outboundDestination: outboundDestination,
		}, "", ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "missing or invalid signature data")
		require.Nil(t, followup)
	})
	t.Run("inbound responses verify signature error", func(t *testing.T) {
		connection := &Connection{
			DID:    newDidDoc.ID,
			DIDDoc: createDIDDocWithKey(pubKey),
		}
		connectionSignature, err := ctx.prepareConnectionSignature(connection)
		require.NoError(t, err)

		// generate different key and assign it to signature verification key
		pubKey2, _ := generateKeyPair()
		connectionSignature.SignVerKey = base64.URLEncoding.EncodeToString(base58.Decode(pubKey2))

		response = &Response{
			Type: RequestMsgType,
			ID:   randomString(),
			Thread: &decorator.Thread{
				ID: "responseID",
			},
			ConnectionSignature: connectionSignature,
		}
		respPayloadBytes, err := json.Marshal(response)
		require.NoError(t, err)
		_, followup, _, err := (&completed{}).ExecuteInbound(&stateMachineMsg{
			header:              &service.Header{Type: ResponseMsgType},
			payload:             respPayloadBytes,
			outboundDestination: outboundDestination,
		}, "", ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "signature doesn't match")
		require.Nil(t, followup)
	})
	t.Run("no followup for inbound responses error", func(t *testing.T) {
		_, followup, _, err := (&completed{}).ExecuteInbound(&stateMachineMsg{
			header: &service.Header{Type: ResponseMsgType},
		}, "", &context{})
		require.Error(t, err)
		require.Nil(t, followup)
	})
	t.Run("no followup for inbound acks", func(t *testing.T) {
		connRec := &ConnectionRecord{State: (&responded{}).Name(), ThreadID: response.Thread.ID, ConnectionID: "123"}
		err := ctx.connectionStore.saveConnectionRecord(connRec)
		require.NoError(t, err)
		err = ctx.connectionStore.saveNSThreadID(response.Thread.ID, findNameSpace(AckMsgType), connRec.ConnectionID)
		require.NoError(t, err)

		ackPayloadBytes, err = json.Marshal(&model.Ack{
			Type:   AckMsgType,
			ID:     randomString(),
			Status: ackStatusOK,
			Thread: &decorator.Thread{
				ID: response.Thread.ID,
			},
		})
		require.NoError(t, err)
		_, followup, _, err := (&completed{}).ExecuteInbound(&stateMachineMsg{
			header:  &service.Header{Type: AckMsgType},
			payload: ackPayloadBytes,
		}, "", ctx)
		require.NoError(t, err)
		require.IsType(t, &noOp{}, followup)
	})
	t.Run("handle inbound response  error", func(t *testing.T) {
		store := &mockstorage.MockStore{Store: make(map[string][]byte)}
		ctx2 := &context{outboundDispatcher: &mockdispatcher.MockOutbound{SendErr: fmt.Errorf("error")},
			didCreator: &mockdid.MockDIDCreator{Doc: getMockDID()}, signer: &mockSigner{},
			connectionStore: NewConnectionRecorder(store, nil), didStore: prov.DIDStore()}
		_, followup, action, err := (&completed{}).
			ExecuteInbound(&stateMachineMsg{header: &service.Header{Type: ResponseMsgType}, payload: responsePayloadBytes,
				outboundDestination: outboundDestination}, "", ctx2)
		require.Error(t, err)
		require.Nil(t, followup)
		require.Nil(t, action)
	})
}
func TestVerifySignature(t *testing.T) {
	pubKey, privKey := generateKeyPair()
	ctx := &context{signer: &mockSigner{privateKey: privKey}}
	newDIDDoc := createDIDDocWithKey(pubKey)
	connection := &Connection{
		DID:    newDIDDoc.ID,
		DIDDoc: newDIDDoc,
	}

	t.Run("signature verified", func(t *testing.T) {
		connectionSignature, err := ctx.prepareConnectionSignature(connection)
		require.NoError(t, err)

		con, err := verifySignature(connectionSignature)
		require.NoError(t, err)
		require.NotNil(t, con)
		require.Equal(t, newDIDDoc.ID, con.DID)
	})
	t.Run("missing/invalid signature data", func(t *testing.T) {
		con, err := verifySignature(&ConnectionSignature{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "missing or invalid signature data")
		require.Nil(t, con)
	})
	t.Run("decode signature data error", func(t *testing.T) {
		connectionSignature, err := ctx.prepareConnectionSignature(connection)
		require.NoError(t, err)

		connectionSignature.SignedData = "invalid-signed-data"
		con, err := verifySignature(connectionSignature)
		require.Error(t, err)
		require.Contains(t, err.Error(), "decode signature data: illegal base64 data")
		require.Nil(t, con)
	})
	t.Run("decode signature error", func(t *testing.T) {
		connectionSignature, err := ctx.prepareConnectionSignature(connection)
		require.NoError(t, err)

		connectionSignature.Signature = "invalid-signature"
		con, err := verifySignature(connectionSignature)
		require.Error(t, err)
		require.Contains(t, err.Error(), "decode signature: illegal base64 data")
		require.Nil(t, con)
	})
	t.Run("decode verification key error ", func(t *testing.T) {
		connectionSignature, err := ctx.prepareConnectionSignature(connection)
		require.NoError(t, err)

		connectionSignature.SignVerKey = "invalid-key"
		con, err := verifySignature(connectionSignature)
		require.Error(t, err)
		require.Contains(t, err.Error(), "decode public key: illegal base64 data")
		require.Nil(t, con)
	})
	t.Run("verify signature error", func(t *testing.T) {
		connectionSignature, err := ctx.prepareConnectionSignature(connection)
		require.NoError(t, err)

		// generate different key and assign it to signature verification key
		pubKey2, _ := generateKeyPair()
		connectionSignature.SignVerKey = base64.URLEncoding.EncodeToString(base58.Decode(pubKey2))
		con, err := verifySignature(connectionSignature)
		require.Error(t, err)
		require.Contains(t, err.Error(), "signature doesn't match")
		require.Nil(t, con)
	})
	t.Run("connection unmarshal error", func(t *testing.T) {
		connAttributeBytes := []byte("{hello world}")

		now := getEpochTime()
		timestamp := strconv.FormatInt(now, 10)
		prefix := append([]byte(timestamp), signatureDataDelimiter)
		concatenateSignData := append(prefix, connAttributeBytes...)

		signature, err := ctx.signer.SignMessage(concatenateSignData, pubKey)
		require.NoError(t, err)

		cs := &ConnectionSignature{
			Type:       "https://didcomm.org/signature/1.0/ed25519Sha512_single",
			SignedData: base64.URLEncoding.EncodeToString(concatenateSignData),
			SignVerKey: base64.URLEncoding.EncodeToString(base58.Decode(pubKey)),
			Signature:  base64.URLEncoding.EncodeToString(signature),
		}

		con, err := verifySignature(cs)
		require.Error(t, err)
		require.Contains(t, err.Error(), "unmarshal failed")
		require.Nil(t, con)
	})
	t.Run("missing connection attribute bytes", func(t *testing.T) {
		now := getEpochTime()
		timestamp := strconv.FormatInt(now, 10)
		prefix := append([]byte(timestamp), signatureDataDelimiter)

		signature, err := ctx.signer.SignMessage(prefix, pubKey)
		require.NoError(t, err)

		cs := &ConnectionSignature{
			Type:       "https://didcomm.org/signature/1.0/ed25519Sha512_single",
			SignedData: base64.URLEncoding.EncodeToString(prefix),
			SignVerKey: base64.URLEncoding.EncodeToString(base58.Decode(pubKey)),
			Signature:  base64.URLEncoding.EncodeToString(signature),
		}

		con, err := verifySignature(cs)
		require.Error(t, err)
		require.Contains(t, err.Error(), "missing connection attribute bytes")
		require.Nil(t, con)
	})
}

func TestPrepareConnectionSignature(t *testing.T) {
	t.Run("prepare connection signature", func(t *testing.T) {
		pubKey, privKey := generateKeyPair()
		ctx := &context{didCreator: &mockdid.MockDIDCreator{Doc: createDIDDocWithKey(pubKey)},
			signer: &mockSigner{privateKey: privKey}}
		newDidDoc, err := ctx.didCreator.Create(testMethod)
		require.NoError(t, err)
		connection := &Connection{
			DID:    newDidDoc.ID,
			DIDDoc: newDidDoc,
		}
		connectionSignature, err := ctx.prepareConnectionSignature(connection)
		require.NoError(t, err)
		require.NotNil(t, connectionSignature)
		sigData, err := base64.URLEncoding.DecodeString(connectionSignature.SignedData)
		require.NoError(t, err)
		connBytes := sigData[bytes.IndexRune(sigData, '{'):]
		sigDataConnection := &Connection{}
		err = json.Unmarshal(connBytes, sigDataConnection)
		require.NoError(t, err)
		require.Equal(t, connection.DID, sigDataConnection.DID)
	})
	t.Run("prepare connection signature error", func(t *testing.T) {
		ctx := &context{signer: &mockSigner{err: errors.New("sign error")}}
		connection := &Connection{
			DIDDoc: getMockDID(),
		}
		connectionSignature, err := ctx.prepareConnectionSignature(connection)
		require.NotNil(t, err)
		require.Contains(t, err.Error(), "sign error")
		require.Nil(t, connectionSignature)
	})
}

func TestPrepareDestination(t *testing.T) {
	prov := protocol.MockProvider{}
	ctx := &context{outboundDispatcher: prov.OutboundDispatcher(), didCreator: &mockdid.MockDIDCreator{Doc: getMockDID()}}
	newDidDoc, err := ctx.didCreator.Create(testMethod)
	require.NoError(t, err)
	dest := prepareDestination(newDidDoc)
	require.NotNil(t, dest)
	require.Equal(t, dest.ServiceEndpoint, "https://localhost:8090")
	// 2 Public keys inside the didDoc
	require.Len(t, dest.RecipientKeys, 3)
}

func TestNewRequestFromInvitation(t *testing.T) {
	t.Run("successful new request from invitation", func(t *testing.T) {
		prov := protocol.MockProvider{}
		store := &mockstorage.MockStore{Store: make(map[string][]byte)}
		ctx := &context{outboundDispatcher: prov.OutboundDispatcher(),
			didCreator:      &mockdid.MockDIDCreator{Doc: getMockDID()},
			connectionStore: NewConnectionRecorder(store, nil),
			didStore:        prov.DIDStore()}
		invitation := &Invitation{
			Type:            InvitationMsgType,
			ID:              randomString(),
			Label:           "Bob",
			RecipientKeys:   []string{"8HH5gYEeNc3z7PYXmd54d4x6qAfCNrqQqEB3nS7Zfu7K"},
			ServiceEndpoint: "https://localhost:8090",
			RoutingKeys:     []string{"8HH5gYEeNc3z7PYXmd54d4x6qAfCNrqQqEB3nS7Zfu7K"},
		}
		invitationBytes, err := json.Marshal(invitation)
		require.NoError(t, err)
		thid, err := threadID(&service.DIDCommMsg{
			Header:  &service.Header{Type: InvitationMsgType},
			Payload: invitationBytes,
		})
		require.NoError(t, err)
		connRec := &ConnectionRecord{State: (&requested{}).Name(), ThreadID: thid, ConnectionID: invitation.ID}
		err = ctx.connectionStore.saveConnectionRecord(connRec)
		require.NoError(t, err)
		err = ctx.connectionStore.saveNSThreadID(thid, findNameSpace(ResponseMsgType), connRec.ConnectionID)
		require.NoError(t, err)
		_, _, err = ctx.handleInboundInvitation(invitation, thid, &ConnectionRecord{})
		require.NoError(t, err)
	})
	t.Run("unsuccessful new request from invitation ", func(t *testing.T) {
		prov := protocol.MockProvider{}
		ctx := &context{outboundDispatcher: prov.OutboundDispatcher(),
			didCreator: &mockdid.MockDIDCreator{Failure: fmt.Errorf("create DID error")}}
		invitation := &Invitation{}
		invitationBytes, err := json.Marshal(invitation)
		require.NoError(t, err)
		thid, err := threadID(&service.DIDCommMsg{
			Header:  &service.Header{Type: InvitationMsgType},
			Payload: invitationBytes,
		})
		require.NoError(t, err)
		_, _, err = ctx.handleInboundInvitation(invitation, thid, &ConnectionRecord{})
		require.Error(t, err)
		require.Equal(t, "create DID error", err.Error())
	})
}

func TestNewResponseFromRequest(t *testing.T) {
	prov := protocol.MockProvider{}
	t.Run("successful new response from request", func(t *testing.T) {
		store := &mockstorage.MockStore{Store: make(map[string][]byte)}
		pubKey, privKey := generateKeyPair()
		ctx := &context{outboundDispatcher: prov.OutboundDispatcher(),
			didCreator:      &mockdid.MockDIDCreator{Doc: createDIDDocWithKey(pubKey)},
			signer:          &mockSigner{privateKey: privKey},
			connectionStore: NewConnectionRecorder(store, nil), didStore: prov.DIDStore()}
		newDidDoc, err := ctx.didCreator.Create(testMethod)
		require.NoError(t, err)
		request := &Request{
			Type:  RequestMsgType,
			ID:    randomString(),
			Label: "Bob",
			Connection: &Connection{
				DID:    newDidDoc.ID,
				DIDDoc: newDidDoc,
			},
		}
		connRec := &ConnectionRecord{State: (&responded{}).Name(), ThreadID: request.ID, ConnectionID: randomString()}
		err = ctx.connectionStore.saveConnectionRecord(connRec)
		require.NoError(t, err)
		err = ctx.connectionStore.saveNSThreadID(request.ID, findNameSpace(RequestMsgType), connRec.ConnectionID)
		require.NoError(t, err)
		_, _, err = ctx.handleInboundRequest(request, &ConnectionRecord{})
		require.NoError(t, err)
	})
	t.Run("unsuccessful new response from request due to create did error", func(t *testing.T) {
		ctx := &context{didCreator: &mockdid.MockDIDCreator{Failure: fmt.Errorf("create DID error")}}
		request := &Request{}
		_, _, err := ctx.handleInboundRequest(request, &ConnectionRecord{})
		require.Error(t, err)
		require.Equal(t, "create DID error", err.Error())
	})
	t.Run("unsuccessful new response from request due to sign error", func(t *testing.T) {
		ctx := &context{didCreator: &mockdid.MockDIDCreator{Doc: getMockDID()},
			signer: &mockSigner{err: errors.New("sign error")}, didStore: prov.DIDStore()}
		request := &Request{}
		_, _, err := ctx.handleInboundRequest(request, &ConnectionRecord{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "sign error")
	})
}

func TestGetPublicKey(t *testing.T) {
	t.Run("successfully getting public key", func(t *testing.T) {
		prov := protocol.MockProvider{}
		ctx := &context{outboundDispatcher: prov.OutboundDispatcher(), didCreator: &mockdid.MockDIDCreator{Doc: getMockDID()}}
		newDidDoc, err := ctx.didCreator.Create(testMethod)
		require.NoError(t, err)
		pubkey, err := getPublicKeys(newDidDoc, supportedPublicKeyType)
		require.NoError(t, err)
		require.NotNil(t, pubkey)
		require.Len(t, pubkey, 1)
	})
	t.Run("failed to get public key", func(t *testing.T) {
		prov := protocol.MockProvider{}
		ctx := &context{outboundDispatcher: prov.OutboundDispatcher(), didCreator: &mockdid.MockDIDCreator{Doc: getMockDID()}}
		newDidDoc, err := ctx.didCreator.Create(testMethod)
		require.NoError(t, err)
		pubkey, err := getPublicKeys(newDidDoc, "invalid key")
		require.Error(t, err)
		require.Contains(t, err.Error(), "public key not supported")
		require.Nil(t, pubkey)
	})
}

func TestGetDestinationFromDID(t *testing.T) {
	t.Run("successfully getting destination from public DID", func(t *testing.T) {
		doc := createDIDDoc()
		ctx := context{didResolver: &mockdidresolver.MockResolver{Doc: doc}}
		destination, err := ctx.getDestinationFromDID(doc.ID)
		require.NoError(t, err)
		require.NotNil(t, destination)
	})
	t.Run("test public key not found", func(t *testing.T) {
		doc := createDIDDoc()
		doc.PublicKey = nil
		ctx := context{didResolver: &mockdidresolver.MockResolver{Doc: doc}}
		destination, err := ctx.getDestinationFromDID(doc.ID)
		require.Error(t, err)
		require.Nil(t, destination)
		require.Contains(t, err.Error(), "public key not supported")
	})
	t.Run("test service not found", func(t *testing.T) {
		doc := createDIDDoc()
		doc.Service = nil
		ctx := context{didResolver: &mockdidresolver.MockResolver{Doc: doc}}
		destination, err := ctx.getDestinationFromDID(doc.ID)
		require.Error(t, err)
		require.Nil(t, destination)
		require.Contains(t, err.Error(), "service not found in DID document")
	})
	t.Run("get destination by invitation", func(t *testing.T) {
		ctx := context{didResolver: &mockdidresolver.MockResolver{Doc: createDIDDoc()}}
		invitation := &Invitation{DID: "test"}
		destination, err := ctx.getDestination(invitation)
		require.NoError(t, err)
		require.NotNil(t, destination)
	})
	t.Run("test did document not found", func(t *testing.T) {
		doc := createDIDDoc()
		ctx := context{didResolver: &mockdidresolver.MockResolver{Err: errors.New("resolver error")}}
		destination, err := ctx.getDestinationFromDID(doc.ID)
		require.Error(t, err)
		require.Nil(t, destination)
		require.Contains(t, err.Error(), "resolver error")
	})
}
func TestPrepareConnectionRecord(t *testing.T) {
	t.Run("prepare ack connection record error", func(t *testing.T) {
		ctx := context{didResolver: &mockdidresolver.MockResolver{Doc: createDIDDoc()}}
		ackPayloadBytes, err := json.Marshal(&model.Ack{
			Type:   AckMsgType,
			ID:     randomString(),
			Status: ackStatusOK,
			Thread: &decorator.Thread{
				ID: "",
			}})
		require.NoError(t, err)
		connRec, err := ctx.prepareAckConnectionRecord(ackPayloadBytes)
		require.Error(t, err)
		require.Nil(t, connRec)

		connRec, err = ctx.prepareAckConnectionRecord([]byte(""))
		require.Error(t, err)
		require.Nil(t, connRec)
	})
	t.Run("prepare response connection record", func(t *testing.T) {
		ctx := context{didResolver: &mockdidresolver.MockResolver{Doc: createDIDDoc()}}
		response := &Response{
			Type: RequestMsgType,
			ID:   randomString(),
			Thread: &decorator.Thread{
				ID: "",
			},
			ConnectionSignature: &ConnectionSignature{},
		}
		respPayloadBytes, err := json.Marshal(response)
		require.NoError(t, err)
		connRec, err := ctx.prepareResponseConnectionRecord(respPayloadBytes)
		require.Contains(t, err.Error(), "empty bytes")
		require.Nil(t, connRec)

		connRec, err = ctx.prepareResponseConnectionRecord([]byte(""))
		require.Error(t, err)
		require.Nil(t, connRec)
	})
	t.Run("prepare request connection record error", func(t *testing.T) {
		connRec, err := prepareRequestConnectionRecord([]byte(""))
		require.Error(t, err)
		require.Nil(t, connRec)
	})
}

type mockSigner struct {
	privateKey []byte
	err        error
}

func (s *mockSigner) SignMessage(message []byte, fromVerKey string) ([]byte, error) {
	if s.privateKey != nil {
		return ed25519.Sign(s.privateKey, message), nil
	}
	return nil, s.err
}

func createDIDDoc() *diddoc.Doc {
	pubKey, _ := generateKeyPair()
	return createDIDDocWithKey(pubKey)
}

func createDIDDocWithKey(pub string) *diddoc.Doc {
	const didFormat = "did:%s:%s"
	const didPKID = "%s#keys-%d"
	const didServiceID = "%s#endpoint-%d"
	const method = "test"

	id := fmt.Sprintf(didFormat, method, pub[:16])
	pubKey := diddoc.PublicKey{
		ID:         fmt.Sprintf(didPKID, id, 1),
		Type:       "Ed25519VerificationKey2018",
		Controller: id,
		Value:      []byte(pub),
	}

	services := []diddoc.Service{
		{
			ID:              fmt.Sprintf(didServiceID, id, 1),
			Type:            "did-communication",
			ServiceEndpoint: "http://localhost:58416",
		},
	}

	createdTime := time.Now()

	didDoc := &diddoc.Doc{
		Context:   []string{diddoc.Context},
		ID:        id,
		PublicKey: []diddoc.PublicKey{pubKey},
		Service:   services,
		Created:   &createdTime,
		Updated:   &createdTime,
	}
	return didDoc
}

func generateKeyPair() (string, []byte) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	return base58.Encode(pubKey[:]), privKey
}
