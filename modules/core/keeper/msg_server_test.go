package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	sdk "github.com/cosmos/cosmos-sdk/types"
	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"
	clienttypes "github.com/cosmos/ibc-go/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/modules/core/04-channel/types"
	commitmenttypes "github.com/cosmos/ibc-go/modules/core/23-commitment/types"
	host "github.com/cosmos/ibc-go/modules/core/24-host"
	"github.com/cosmos/ibc-go/modules/core/exported"
	"github.com/cosmos/ibc-go/modules/core/keeper"
	ibctmtypes "github.com/cosmos/ibc-go/modules/light-clients/07-tendermint/types"
	ibctesting "github.com/cosmos/ibc-go/testing"
	ibcmock "github.com/cosmos/ibc-go/testing/mock"
)

const height = 10

var (
	timeoutHeight = clienttypes.NewHeight(0, 10000)
	maxSequence   = uint64(10)
)

type KeeperTestSuite struct {
	suite.Suite

	coordinator *ibctesting.Coordinator

	chainA *ibctesting.TestChain
	chainB *ibctesting.TestChain
}

// SetupTest creates a coordinator with 2 test chains.
func (suite *KeeperTestSuite) SetupTest() {
	suite.coordinator = ibctesting.NewCoordinator(suite.T(), 2)

	suite.chainA = suite.coordinator.GetChain(ibctesting.GetChainID(0))
	suite.chainB = suite.coordinator.GetChain(ibctesting.GetChainID(1))
	// commit some blocks so that QueryProof returns valid proof (cannot return valid query if height <= 1)
	suite.coordinator.CommitNBlocks(suite.chainA, 2)
	suite.coordinator.CommitNBlocks(suite.chainB, 2)
}

func TestIBCTestSuite(t *testing.T) {
	suite.Run(t, new(KeeperTestSuite))
}

// tests the IBC handler receiving a packet on ordered and unordered channels.
// It verifies that the storing of an acknowledgement on success occurs. It
// tests high level properties like ordering and basic sanity checks. More
// rigorous testing of 'RecvPacket' can be found in the
// 04-channel/keeper/packet_test.go.
func (suite *KeeperTestSuite) TestHandleRecvPacket() {
	var (
		packet channeltypes.Packet
		async  bool // indicate no ack written
	)

	testCases := []struct {
		name      string
		malleate  func()
		expPass   bool
		expRevert bool
	}{
		{"success: ORDERED", func() {
			_, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.ORDERED)
			packet = channeltypes.NewPacket(ibctesting.MockPacketData, 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)

			err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
			suite.Require().NoError(err)
		}, true, false},
		{"success: UNORDERED", func() {
			_, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.UNORDERED)
			packet = channeltypes.NewPacket(ibctesting.MockPacketData, 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)

			err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
			suite.Require().NoError(err)
		}, true, false},
		{"success: UNORDERED out of order packet", func() {
			// setup uses an UNORDERED channel
			_, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.UNORDERED)

			// attempts to receive packet with sequence 10 without receiving packet with sequence 1
			for i := uint64(1); i < 10; i++ {
				packet = channeltypes.NewPacket(ibctesting.MockPacketData, i, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)

				err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
				suite.Require().NoError(err)
			}
		}, true, false},
		{"success: OnRecvPacket callback returns revert=true", func() {
			_, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.UNORDERED)
			packet = channeltypes.NewPacket(ibctesting.MockFailPacketData, 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)

			err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
			suite.Require().NoError(err)
		}, true, true},
		{"success: ORDERED - async acknowledgement", func() {
			async = true
			_, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.ORDERED)
			packet = channeltypes.NewPacket(ibcmock.MockAsyncPacketData, 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)

			err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
			suite.Require().NoError(err)
		}, true, false},
		{"success: UNORDERED - async acknowledgement", func() {
			async = true
			_, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.UNORDERED)
			packet = channeltypes.NewPacket(ibcmock.MockAsyncPacketData, 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)

			err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
			suite.Require().NoError(err)
		}, true, false},
		{"failure: ORDERED out of order packet", func() {
			_, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.ORDERED)

			// attempts to receive packet with sequence 10 without receiving packet with sequence 1
			for i := uint64(1); i < 10; i++ {
				packet = channeltypes.NewPacket(ibctesting.MockPacketData, i, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)

				err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
				suite.Require().NoError(err)
			}
		}, false, false},
		{"channel does not exist", func() {
			// any non-nil value of packet is valid
			suite.Require().NotNil(packet)
		}, false, false},
		{"packet not sent", func() {
			_, _, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.UNORDERED)
			packet = channeltypes.NewPacket(ibctesting.MockPacketData, 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)
		}, false, false},
		{"ORDERED: packet already received (replay)", func() {
			clientA, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.ORDERED)
			packet = channeltypes.NewPacket(ibctesting.MockPacketData, 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)

			err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
			suite.Require().NoError(err)

			err = suite.coordinator.RecvPacket(suite.chainA, suite.chainB, clientA, packet)
			suite.Require().NoError(err)
		}, false, false},
		{"UNORDERED: packet already received (replay)", func() {
			clientA, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.UNORDERED)

			packet = channeltypes.NewPacket(ibctesting.MockPacketData, 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)

			err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
			suite.Require().NoError(err)

			err = suite.coordinator.RecvPacket(suite.chainA, suite.chainB, clientA, packet)
			suite.Require().NoError(err)
		}, false, false},
	}

	for _, tc := range testCases {
		tc := tc

		suite.Run(tc.name, func() {
			suite.SetupTest() // reset
			async = false     // reset

			tc.malleate()

			// get proof of packet commitment from chainA
			packetKey := host.PacketCommitmentKey(packet.GetSourcePort(), packet.GetSourceChannel(), packet.GetSequence())
			proof, proofHeight := suite.chainA.QueryProof(packetKey)

			msg := channeltypes.NewMsgRecvPacket(packet, proof, proofHeight, suite.chainB.SenderAccount.GetAddress().String())

			_, err := keeper.Keeper.RecvPacket(*suite.chainB.App.IBCKeeper, sdk.WrapSDKContext(suite.chainB.GetContext()), msg)

			if tc.expPass {
				suite.Require().NoError(err)

				// replay should fail since state changes occur
				_, err := keeper.Keeper.RecvPacket(*suite.chainB.App.IBCKeeper, sdk.WrapSDKContext(suite.chainB.GetContext()), msg)
				suite.Require().Error(err)

				// check that callback state was handled correctly
				_, exists := suite.chainB.App.ScopedIBCMockKeeper.GetCapability(suite.chainB.GetContext(), ibctesting.MockCanaryCapabilityName)
				if tc.expRevert {
					suite.Require().False(exists, "capability exists in store even after callback reverted")
				} else {
					suite.Require().True(exists, "callback state not persisted when revert is false")
				}

				// verify if ack was written
				ack, found := suite.chainB.App.IBCKeeper.ChannelKeeper.GetPacketAcknowledgement(suite.chainB.GetContext(), packet.GetDestPort(), packet.GetDestChannel(), packet.GetSequence())

				if async {
					suite.Require().Nil(ack)
					suite.Require().False(found)

				} else {
					suite.Require().NotNil(ack)
					suite.Require().True(found)
				}
			} else {
				suite.Require().Error(err)
			}
		})
	}
}

// tests the IBC handler acknowledgement of a packet on ordered and unordered
// channels. It verifies that the deletion of packet commitments from state
// occurs. It test high level properties like ordering and basic sanity
// checks. More rigorous testing of 'AcknowledgePacket'
// can be found in the 04-channel/keeper/packet_test.go.
func (suite *KeeperTestSuite) TestHandleAcknowledgePacket() {
	var (
		packet channeltypes.Packet
	)

	testCases := []struct {
		name     string
		malleate func()
		expPass  bool
	}{
		{"success: ORDERED", func() {
			clientA, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.ORDERED)
			packet = channeltypes.NewPacket(ibctesting.MockPacketData, 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)

			err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
			suite.Require().NoError(err)

			err = suite.coordinator.RecvPacket(suite.chainA, suite.chainB, clientA, packet)
			suite.Require().NoError(err)
		}, true},
		{"success: UNORDERED", func() {
			clientA, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.UNORDERED)
			packet = channeltypes.NewPacket(ibctesting.MockPacketData, 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)

			err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
			suite.Require().NoError(err)

			err = suite.coordinator.RecvPacket(suite.chainA, suite.chainB, clientA, packet)
			suite.Require().NoError(err)
		}, true},
		{"success: UNORDERED acknowledge out of order packet", func() {
			// setup uses an UNORDERED channel
			clientA, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.UNORDERED)

			// attempts to acknowledge ack with sequence 10 without acknowledging ack with sequence 1 (removing packet commitment)
			for i := uint64(1); i < 10; i++ {
				packet = channeltypes.NewPacket(ibctesting.MockPacketData, i, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)

				err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
				suite.Require().NoError(err)

				err = suite.coordinator.RecvPacket(suite.chainA, suite.chainB, clientA, packet)
				suite.Require().NoError(err)
			}
		}, true},
		{"failure: ORDERED acknowledge out of order packet", func() {
			clientA, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.ORDERED)

			// attempts to acknowledge ack with sequence 10 without acknowledging ack with sequence 1 (removing packet commitment
			for i := uint64(1); i < 10; i++ {
				packet = channeltypes.NewPacket(ibctesting.MockPacketData, i, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)

				err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
				suite.Require().NoError(err)

				err = suite.coordinator.RecvPacket(suite.chainA, suite.chainB, clientA, packet)
				suite.Require().NoError(err)
			}
		}, false},
		{"channel does not exist", func() {
			// any non-nil value of packet is valid
			suite.Require().NotNil(packet)
		}, false},
		{"packet not received", func() {
			_, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.UNORDERED)
			packet = channeltypes.NewPacket(ibctesting.MockPacketData, 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)

			err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
			suite.Require().NoError(err)
		}, false},
		{"ORDERED: packet already acknowledged (replay)", func() {
			clientA, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.ORDERED)
			packet = channeltypes.NewPacket(ibctesting.MockPacketData, 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)

			err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
			suite.Require().NoError(err)

			err = suite.coordinator.RecvPacket(suite.chainA, suite.chainB, clientA, packet)
			suite.Require().NoError(err)

			err = suite.coordinator.AcknowledgePacket(suite.chainA, suite.chainB, clientB, packet, ibctesting.MockAcknowledgement)
			suite.Require().NoError(err)
		}, false},
		{"UNORDERED: packet already acknowledged (replay)", func() {
			clientA, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.UNORDERED)

			packet = channeltypes.NewPacket(ibctesting.MockPacketData, 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)

			err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
			suite.Require().NoError(err)

			err = suite.coordinator.RecvPacket(suite.chainA, suite.chainB, clientA, packet)
			suite.Require().NoError(err)

			err = suite.coordinator.AcknowledgePacket(suite.chainA, suite.chainB, clientB, packet, ibctesting.MockAcknowledgement)
			suite.Require().NoError(err)
		}, false},
	}

	for _, tc := range testCases {
		tc := tc

		suite.Run(tc.name, func() {
			suite.SetupTest() // reset

			tc.malleate()

			packetKey := host.PacketAcknowledgementKey(packet.GetDestPort(), packet.GetDestChannel(), packet.GetSequence())
			proof, proofHeight := suite.chainB.QueryProof(packetKey)

			msg := channeltypes.NewMsgAcknowledgement(packet, ibcmock.MockAcknowledgement.Acknowledgement(), proof, proofHeight, suite.chainA.SenderAccount.GetAddress().String())

			_, err := keeper.Keeper.Acknowledgement(*suite.chainA.App.IBCKeeper, sdk.WrapSDKContext(suite.chainA.GetContext()), msg)

			if tc.expPass {
				suite.Require().NoError(err)

				// replay should an error
				_, err := keeper.Keeper.Acknowledgement(*suite.chainA.App.IBCKeeper, sdk.WrapSDKContext(suite.chainA.GetContext()), msg)
				suite.Require().Error(err)

				// verify packet commitment was deleted on source chain
				has := suite.chainA.App.IBCKeeper.ChannelKeeper.HasPacketCommitment(suite.chainA.GetContext(), packet.GetSourcePort(), packet.GetSourceChannel(), packet.GetSequence())
				suite.Require().False(has)

			} else {
				suite.Require().Error(err)
			}
		})
	}
}

// tests the IBC handler timing out a packet on ordered and unordered channels.
// It verifies that the deletion of a packet commitment occurs. It tests
// high level properties like ordering and basic sanity checks. More
// rigorous testing of 'TimeoutPacket' and 'TimeoutExecuted' can be found in
// the 04-channel/keeper/timeout_test.go.
func (suite *KeeperTestSuite) TestHandleTimeoutPacket() {
	var (
		packet    channeltypes.Packet
		packetKey []byte
	)

	testCases := []struct {
		name     string
		malleate func()
		expPass  bool
	}{
		{"success: ORDERED", func() {
			clientA, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.ORDERED)
			packet = channeltypes.NewPacket(ibctesting.MockPacketData, 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, clienttypes.GetSelfHeight(suite.chainB.GetContext()), uint64(suite.chainB.GetContext().BlockTime().UnixNano()))

			// create packet commitment
			err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
			suite.Require().NoError(err)

			// need to update chainA client to prove missing ack
			suite.coordinator.UpdateClient(suite.chainA, suite.chainB, clientA, exported.Tendermint)

			packetKey = host.NextSequenceRecvKey(packet.GetDestPort(), packet.GetDestChannel())
		}, true},
		{"success: UNORDERED", func() {
			clientA, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.UNORDERED)
			packet = channeltypes.NewPacket(ibctesting.MockPacketData, 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, clienttypes.GetSelfHeight(suite.chainB.GetContext()), uint64(suite.chainB.GetContext().BlockTime().UnixNano()))

			// create packet commitment
			err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
			suite.Require().NoError(err)

			// need to update chainA client to prove missing ack
			suite.coordinator.UpdateClient(suite.chainA, suite.chainB, clientA, exported.Tendermint)

			packetKey = host.PacketReceiptKey(packet.GetDestPort(), packet.GetDestChannel(), packet.GetSequence())
		}, true},
		{"success: UNORDERED timeout out of order packet", func() {
			// setup uses an UNORDERED channel
			clientA, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.UNORDERED)

			// attempts to timeout the last packet sent without timing out the first packet
			// packet sequences begin at 1
			for i := uint64(1); i < maxSequence; i++ {
				packet = channeltypes.NewPacket(ibctesting.MockPacketData, i, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, clienttypes.GetSelfHeight(suite.chainB.GetContext()), 0)

				// create packet commitment
				err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
				suite.Require().NoError(err)
			}

			suite.coordinator.UpdateClient(suite.chainA, suite.chainB, clientA, exported.Tendermint)
			packetKey = host.PacketReceiptKey(packet.GetDestPort(), packet.GetDestChannel(), packet.GetSequence())
		}, true},
		{"success: ORDERED timeout out of order packet", func() {
			clientA, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.ORDERED)

			// attempts to timeout the last packet sent without timing out the first packet
			// packet sequences begin at 1
			for i := uint64(1); i < maxSequence; i++ {
				packet = channeltypes.NewPacket(ibctesting.MockPacketData, i, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, clienttypes.GetSelfHeight(suite.chainB.GetContext()), 0)

				// create packet commitment
				err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
				suite.Require().NoError(err)
			}

			suite.coordinator.UpdateClient(suite.chainA, suite.chainB, clientA, exported.Tendermint)
			packetKey = host.NextSequenceRecvKey(packet.GetDestPort(), packet.GetDestChannel())

		}, true},
		{"channel does not exist", func() {
			// any non-nil value of packet is valid
			suite.Require().NotNil(packet)

			packetKey = host.NextSequenceRecvKey(packet.GetDestPort(), packet.GetDestChannel())
		}, false},
		{"UNORDERED: packet not sent", func() {
			_, _, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.UNORDERED)
			packet = channeltypes.NewPacket(ibctesting.MockPacketData, 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)
			packetKey = host.PacketReceiptKey(packet.GetDestPort(), packet.GetDestChannel(), packet.GetSequence())
		}, false},
	}

	for _, tc := range testCases {
		tc := tc

		suite.Run(tc.name, func() {
			suite.SetupTest() // reset

			tc.malleate()

			proof, proofHeight := suite.chainB.QueryProof(packetKey)

			msg := channeltypes.NewMsgTimeout(packet, 1, proof, proofHeight, suite.chainA.SenderAccount.GetAddress().String())

			_, err := keeper.Keeper.Timeout(*suite.chainA.App.IBCKeeper, sdk.WrapSDKContext(suite.chainA.GetContext()), msg)

			if tc.expPass {
				suite.Require().NoError(err)

				// replay should return an error
				_, err := keeper.Keeper.Timeout(*suite.chainA.App.IBCKeeper, sdk.WrapSDKContext(suite.chainA.GetContext()), msg)
				suite.Require().Error(err)

				// verify packet commitment was deleted on source chain
				has := suite.chainA.App.IBCKeeper.ChannelKeeper.HasPacketCommitment(suite.chainA.GetContext(), packet.GetSourcePort(), packet.GetSourceChannel(), packet.GetSequence())
				suite.Require().False(has)

			} else {
				suite.Require().Error(err)
			}
		})
	}
}

// tests the IBC handler timing out a packet via channel closure on ordered
// and unordered channels. It verifies that the deletion of a packet
// commitment occurs. It tests high level properties like ordering and basic
// sanity checks. More rigorous testing of 'TimeoutOnClose' and
//'TimeoutExecuted' can be found in the 04-channel/keeper/timeout_test.go.
func (suite *KeeperTestSuite) TestHandleTimeoutOnClosePacket() {
	var (
		packet              channeltypes.Packet
		packetKey           []byte
		counterpartyChannel ibctesting.TestChannel
	)

	testCases := []struct {
		name     string
		malleate func()
		expPass  bool
	}{
		{"success: ORDERED", func() {
			clientA, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.ORDERED)
			packet = channeltypes.NewPacket(ibctesting.MockPacketData, 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)
			counterpartyChannel = ibctesting.TestChannel{
				PortID:               channelB.PortID,
				ID:                   channelB.ID,
				CounterpartyClientID: clientA,
			}

			// create packet commitment
			err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
			suite.Require().NoError(err)

			// need to update chainA client to prove missing ack
			suite.coordinator.UpdateClient(suite.chainA, suite.chainB, clientA, exported.Tendermint)

			packetKey = host.NextSequenceRecvKey(packet.GetDestPort(), packet.GetDestChannel())

			// close counterparty channel
			suite.coordinator.SetChannelClosed(suite.chainB, suite.chainA, counterpartyChannel)
		}, true},
		{"success: UNORDERED", func() {
			clientA, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.UNORDERED)
			packet = channeltypes.NewPacket(ibctesting.MockPacketData, 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)
			counterpartyChannel = ibctesting.TestChannel{
				PortID:               channelB.PortID,
				ID:                   channelB.ID,
				CounterpartyClientID: clientA,
			}

			// create packet commitment
			err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
			suite.Require().NoError(err)

			// need to update chainA client to prove missing ack
			suite.coordinator.UpdateClient(suite.chainA, suite.chainB, clientA, exported.Tendermint)

			packetKey = host.PacketReceiptKey(packet.GetDestPort(), packet.GetDestChannel(), packet.GetSequence())

			// close counterparty channel
			suite.coordinator.SetChannelClosed(suite.chainB, suite.chainA, counterpartyChannel)
		}, true},
		{"success: UNORDERED timeout out of order packet", func() {
			// setup uses an UNORDERED channel
			clientA, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.UNORDERED)
			counterpartyChannel = ibctesting.TestChannel{
				PortID:               channelB.PortID,
				ID:                   channelB.ID,
				CounterpartyClientID: clientA,
			}

			// attempts to timeout the last packet sent without timing out the first packet
			// packet sequences begin at 1
			for i := uint64(1); i < maxSequence; i++ {
				packet = channeltypes.NewPacket(ibctesting.MockPacketData, i, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)

				// create packet commitment
				err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
				suite.Require().NoError(err)
			}

			suite.coordinator.UpdateClient(suite.chainA, suite.chainB, clientA, exported.Tendermint)
			packetKey = host.PacketReceiptKey(packet.GetDestPort(), packet.GetDestChannel(), packet.GetSequence())

			// close counterparty channel
			suite.coordinator.SetChannelClosed(suite.chainB, suite.chainA, counterpartyChannel)
		}, true},
		{"success: ORDERED timeout out of order packet", func() {
			clientA, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.ORDERED)
			counterpartyChannel = ibctesting.TestChannel{
				PortID:               channelB.PortID,
				ID:                   channelB.ID,
				CounterpartyClientID: clientA,
			}

			// attempts to timeout the last packet sent without timing out the first packet
			// packet sequences begin at 1
			for i := uint64(1); i < maxSequence; i++ {
				packet = channeltypes.NewPacket(ibctesting.MockPacketData, i, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)

				// create packet commitment
				err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
				suite.Require().NoError(err)
			}

			suite.coordinator.UpdateClient(suite.chainA, suite.chainB, clientA, exported.Tendermint)
			packetKey = host.NextSequenceRecvKey(packet.GetDestPort(), packet.GetDestChannel())

			// close counterparty channel
			suite.coordinator.SetChannelClosed(suite.chainB, suite.chainA, counterpartyChannel)
		}, true},
		{"channel does not exist", func() {
			// any non-nil value of packet is valid
			suite.Require().NotNil(packet)

			packetKey = host.NextSequenceRecvKey(packet.GetDestPort(), packet.GetDestChannel())
		}, false},
		{"UNORDERED: packet not sent", func() {
			clientA, _, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.UNORDERED)
			packet = channeltypes.NewPacket(ibctesting.MockPacketData, 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)
			packetKey = host.PacketAcknowledgementKey(packet.GetDestPort(), packet.GetDestChannel(), packet.GetSequence())
			counterpartyChannel = ibctesting.TestChannel{
				PortID:               channelB.PortID,
				ID:                   channelB.ID,
				CounterpartyClientID: clientA,
			}

			// close counterparty channel
			suite.coordinator.SetChannelClosed(suite.chainB, suite.chainA, counterpartyChannel)
		}, false},
		{"ORDERED: channel not closed", func() {
			clientA, clientB, _, _, channelA, channelB := suite.coordinator.Setup(suite.chainA, suite.chainB, channeltypes.ORDERED)
			packet = channeltypes.NewPacket(ibctesting.MockPacketData, 1, channelA.PortID, channelA.ID, channelB.PortID, channelB.ID, timeoutHeight, 0)
			counterpartyChannel = ibctesting.TestChannel{
				PortID:               channelB.PortID,
				ID:                   channelB.ID,
				CounterpartyClientID: clientA,
			}

			// create packet commitment
			err := suite.coordinator.SendPacket(suite.chainA, suite.chainB, packet, clientB)
			suite.Require().NoError(err)

			// need to update chainA client to prove missing ack
			suite.coordinator.UpdateClient(suite.chainA, suite.chainB, clientA, exported.Tendermint)

			packetKey = host.NextSequenceRecvKey(packet.GetDestPort(), packet.GetDestChannel())
		}, false},
	}

	for _, tc := range testCases {
		tc := tc

		suite.Run(tc.name, func() {
			suite.SetupTest() // reset

			tc.malleate()

			proof, proofHeight := suite.chainB.QueryProof(packetKey)

			channelKey := host.ChannelKey(counterpartyChannel.PortID, counterpartyChannel.ID)
			proofClosed, _ := suite.chainB.QueryProof(channelKey)

			msg := channeltypes.NewMsgTimeoutOnClose(packet, 1, proof, proofClosed, proofHeight, suite.chainA.SenderAccount.GetAddress().String())

			_, err := keeper.Keeper.TimeoutOnClose(*suite.chainA.App.IBCKeeper, sdk.WrapSDKContext(suite.chainA.GetContext()), msg)

			if tc.expPass {
				suite.Require().NoError(err)

				// replay should return an error
				_, err := keeper.Keeper.TimeoutOnClose(*suite.chainA.App.IBCKeeper, sdk.WrapSDKContext(suite.chainA.GetContext()), msg)
				suite.Require().Error(err)

				// verify packet commitment was deleted on source chain
				has := suite.chainA.App.IBCKeeper.ChannelKeeper.HasPacketCommitment(suite.chainA.GetContext(), packet.GetSourcePort(), packet.GetSourceChannel(), packet.GetSequence())
				suite.Require().False(has)

			} else {
				suite.Require().Error(err)
			}
		})
	}
}

func (suite *KeeperTestSuite) TestUpgradeClient() {
	var (
		clientA           string
		upgradedClient    exported.ClientState
		upgradedConsState exported.ConsensusState
		lastHeight        exported.Height
		msg               *clienttypes.MsgUpgradeClient
	)

	newClientHeight := clienttypes.NewHeight(1, 1)

	cases := []struct {
		name    string
		setup   func()
		expPass bool
	}{
		{
			name: "successful upgrade",
			setup: func() {

				upgradedClient = ibctmtypes.NewClientState("newChainId", ibctmtypes.DefaultTrustLevel, ibctesting.TrustingPeriod, ibctesting.UnbondingPeriod+ibctesting.TrustingPeriod, ibctesting.MaxClockDrift, newClientHeight, commitmenttypes.GetSDKSpecs(), ibctesting.UpgradePath, false, false)
				// Call ZeroCustomFields on upgraded clients to clear any client-chosen parameters in test-case upgradedClient
				upgradedClient = upgradedClient.ZeroCustomFields()

				upgradedConsState = &ibctmtypes.ConsensusState{
					NextValidatorsHash: []byte("nextValsHash"),
				}

				// last Height is at next block
				lastHeight = clienttypes.NewHeight(0, uint64(suite.chainB.GetContext().BlockHeight()+1))

				upgradedClientBz, err := clienttypes.MarshalClientState(suite.chainA.App.AppCodec(), upgradedClient)
				suite.Require().NoError(err)
				upgradedConsStateBz, err := clienttypes.MarshalConsensusState(suite.chainA.App.AppCodec(), upgradedConsState)
				suite.Require().NoError(err)

				// zero custom fields and store in upgrade store
				suite.chainB.App.UpgradeKeeper.SetUpgradedClient(suite.chainB.GetContext(), int64(lastHeight.GetRevisionHeight()), upgradedClientBz)
				suite.chainB.App.UpgradeKeeper.SetUpgradedConsensusState(suite.chainB.GetContext(), int64(lastHeight.GetRevisionHeight()), upgradedConsStateBz)

				// commit upgrade store changes and update clients
				suite.coordinator.CommitBlock(suite.chainB)
				err = suite.coordinator.UpdateClient(suite.chainA, suite.chainB, clientA, exported.Tendermint)
				suite.Require().NoError(err)

				cs, found := suite.chainA.App.IBCKeeper.ClientKeeper.GetClientState(suite.chainA.GetContext(), clientA)
				suite.Require().True(found)

				proofUpgradeClient, _ := suite.chainB.QueryUpgradeProof(upgradetypes.UpgradedClientKey(int64(lastHeight.GetRevisionHeight())), cs.GetLatestHeight().GetRevisionHeight())
				proofUpgradedConsState, _ := suite.chainB.QueryUpgradeProof(upgradetypes.UpgradedConsStateKey(int64(lastHeight.GetRevisionHeight())), cs.GetLatestHeight().GetRevisionHeight())

				msg, err = clienttypes.NewMsgUpgradeClient(clientA, upgradedClient, upgradedConsState,
					proofUpgradeClient, proofUpgradedConsState, suite.chainA.SenderAccount.GetAddress().String())
				suite.Require().NoError(err)
			},
			expPass: true,
		},
		{
			name: "VerifyUpgrade fails",
			setup: func() {

				upgradedClient = ibctmtypes.NewClientState("newChainId", ibctmtypes.DefaultTrustLevel, ibctesting.TrustingPeriod, ibctesting.UnbondingPeriod+ibctesting.TrustingPeriod, ibctesting.MaxClockDrift, newClientHeight, commitmenttypes.GetSDKSpecs(), ibctesting.UpgradePath, false, false)
				// Call ZeroCustomFields on upgraded clients to clear any client-chosen parameters in test-case upgradedClient
				upgradedClient = upgradedClient.ZeroCustomFields()

				upgradedConsState = &ibctmtypes.ConsensusState{
					NextValidatorsHash: []byte("nextValsHash"),
				}

				// last Height is at next block
				lastHeight = clienttypes.NewHeight(0, uint64(suite.chainB.GetContext().BlockHeight()+1))

				upgradedClientBz, err := clienttypes.MarshalClientState(suite.chainA.App.AppCodec(), upgradedClient)
				suite.Require().NoError(err)
				upgradedConsStateBz, err := clienttypes.MarshalConsensusState(suite.chainA.App.AppCodec(), upgradedConsState)
				suite.Require().NoError(err)

				// zero custom fields and store in upgrade store
				suite.chainB.App.UpgradeKeeper.SetUpgradedClient(suite.chainB.GetContext(), int64(lastHeight.GetRevisionHeight()), upgradedClientBz)
				suite.chainB.App.UpgradeKeeper.SetUpgradedConsensusState(suite.chainB.GetContext(), int64(lastHeight.GetRevisionHeight()), upgradedConsStateBz)

				// commit upgrade store changes and update clients
				suite.coordinator.CommitBlock(suite.chainB)
				err = suite.coordinator.UpdateClient(suite.chainA, suite.chainB, clientA, exported.Tendermint)
				suite.Require().NoError(err)

				msg, err = clienttypes.NewMsgUpgradeClient(clientA, upgradedClient, upgradedConsState, nil, nil, suite.chainA.SenderAccount.GetAddress().String())
				suite.Require().NoError(err)
			},
			expPass: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		clientA, _ = suite.coordinator.SetupClients(suite.chainA, suite.chainB, exported.Tendermint)

		tc.setup()

		_, err := keeper.Keeper.UpgradeClient(*suite.chainA.App.IBCKeeper, sdk.WrapSDKContext(suite.chainA.GetContext()), msg)

		if tc.expPass {
			suite.Require().NoError(err, "upgrade handler failed on valid case: %s", tc.name)
			newClient, ok := suite.chainA.App.IBCKeeper.ClientKeeper.GetClientState(suite.chainA.GetContext(), clientA)
			suite.Require().True(ok)
			newChainSpecifiedClient := newClient.ZeroCustomFields()
			suite.Require().Equal(upgradedClient, newChainSpecifiedClient)
		} else {
			suite.Require().Error(err, "upgrade handler passed on invalid case: %s", tc.name)
		}
	}
}
