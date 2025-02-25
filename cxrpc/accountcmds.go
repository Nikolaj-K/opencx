package cxrpc

import (
	"fmt"

	"github.com/mit-dci/lit/coinparam"
	"github.com/mit-dci/lit/crypto/koblitz"
	"github.com/mit-dci/opencx/logging"
)

// RegisterArgs holds the args for register
type RegisterArgs struct {
	Signature []byte
}

// RegisterReply holds the data for the register reply
type RegisterReply struct {
	// empty
}

// Register registers a pubkey into the db, verifies that the action was signed by that pubkey. A valid signature for the string "register" is considered a valid registration.
func (cl *OpencxRPC) Register(args RegisterArgs, reply *RegisterReply) (err error) {

	var pubkey *koblitz.PublicKey
	if pubkey, err = cl.Server.RegistrationStringVerify(args.Signature); err != nil {
		return
	}

	defer func() {
		if err != nil {
			err = fmt.Errorf("Error registering user: \n%s", err)
		}
	}()

	// go through each enabled wallet in the server and create a new address for them.
	addrMap := make(map[*coinparam.Params]string)
	for param := range cl.Server.WalletMap {
		if addrMap[param], err = cl.Server.GetAddrForCoin(param, pubkey); err != nil {
			return
		}
	}

	// Do all this locking just cause
	cl.Server.LockIngests()
	// Insert them into the DB
	if err = cl.Server.OpencxDB.RegisterUser(pubkey, addrMap); err != nil {
		// gotta put these here cause if it errors out then oops just locked everything
		cl.Server.UnlockIngests()
		return
	}
	cl.Server.UnlockIngests()

	logging.Infof("Registering user with pubkey %x\n", pubkey.SerializeCompressed())
	// put this in database

	return
}

// GetRegistrationStringArgs holds the args for register
type GetRegistrationStringArgs struct {
	// empty
}

// GetRegistrationStringReply holds the data for the register reply
type GetRegistrationStringReply struct {
	RegistrationString string
}

// GetRegistrationString returns a string to the client which is a valid string to sign to indicate they want their pubkey to be registered. This is like kinda weird but whatever
func (cl *OpencxRPC) GetRegistrationString(args GetRegistrationStringArgs, reply *GetRegistrationStringReply) (err error) {
	reply.RegistrationString = cl.Server.GetRegistrationString()
	return
}
