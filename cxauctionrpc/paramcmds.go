package cxauctionrpc

import "fmt"

// GetPublicParametersArgs holds the args for the getpublicparameters command
type GetPublicParametersArgs struct {
	// empty
}

// GetPublicParametersReply holds the reply for the getpublicparameters command
type GetPublicParametersReply struct {
	AuctionID [32]byte
	// This is the time that it will take the auction to run. We need to make sure it doesn't
	// take any less than this, and can actually verify that the exchange isn't running it
	// for extra time.
	AuctionTime uint64
}

// GetPublicParameters gets public parameters from the exchange, like time and auctionID
func (cl *OpencxAuctionRPC) GetPublicParameters(args GetPublicParametersArgs, reply *GetPublicParametersReply) (err error) {
	if reply.AuctionID, err = cl.Server.CurrentAuctionID(); err != nil {
		err = fmt.Errorf("Error getting public param auction id: %s", err)
		return
	}

	if reply.AuctionTime, err = cl.Server.CurrentAuctionTime(); err != nil {
		err = fmt.Errorf("Error getting public param auction time: %s", err)
		return
	}

	return
}
