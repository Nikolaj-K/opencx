package main

import (
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"

	"github.com/mit-dci/lit/crypto/koblitz"

	flags "github.com/jessevdk/go-flags"
	"github.com/mit-dci/opencx/cxauctionrpc"
	"github.com/mit-dci/opencx/cxauctionserver"
	"github.com/mit-dci/opencx/cxdb/cxdbsql"
	"github.com/mit-dci/opencx/logging"
)

type fredConfig struct {
	ConfigFile string

	// stuff for files and directories
	LogFilename string `long:"logFilename" description:"Filename for output log file"`
	FredHomeDir string `long:"dir" description:"Location of the root directory relative to home directory"`

	// stuff for ports
	Rpcport uint16 `short:"p" long:"rpcport" description:"Set RPC port to connect to"`
	Rpchost string `long:"rpchost" description:"Set RPC host to listen to"`

	// logging and debug parameters
	LogLevel []bool `short:"v" description:"Set verbosity level to verbose (-v), very verbose (-vv) or very very verbose (-vvv)"`

	// logging for lit nodes (find something better than w)
	LitLogLevel []bool `short:"w" description:"Set verbosity level to verbose (-w), very verbose (-ww) or very very verbose (-www)"`

	// Resync?
	Resync bool `short:"r" long:"resync" description:"Do you want to resync all chains?"`

	// networks that we can connect to
	Vtchost     string `long:"vtc" description:"Connect to Vertcoin full node. Specify a socket address."`
	Btchost     string `long:"btc" description:"Connect to bitcoin full node. Specify a socket address."`
	Ltchost     string `long:"ltc" description:"Connect to a litecoin full node. Specify a socket address."`
	Tn3host     string `long:"tn3" description:"Connect to bitcoin testnet3. Specify a socket address."`
	Lt4host     string `long:"lt4" description:"Connect to litecoin testnet4. Specify a socket address."`
	Tvtchost    string `long:"tvtc" description:"Connect to Vertcoin test node. Specify a socket address."`
	Reghost     string `long:"reg" description:"Connect to bitcoin regtest. Specify a socket address."`
	Litereghost string `long:"litereg" description:"Connect to litecoin regtest. Specify a socket address."`
	Rtvtchost   string `long:"rtvtc" description:"Connect to Vertcoin regtest node. Specify a socket address."`

	// configuration for concurrent RPC users.
	MaxPeers    uint16 `long:"numpeers" description:"Maximum number of peers that you'd like to support"`
	MinPeerPort uint16 `long:"minpeerport" description:"Port to start creating ports for peers at"`
	Lithost     string `long:"lithost" description:"Host for the lightning node on the exchange to run"`
	Litport     uint16 `long:"litport" description:"Port for the lightning node on the exchange to run"`

	// filename for key
	KeyFileName string `long:"keyfilename" short:"k" description:"Filename for private key within root opencx directory used to send transactions"`

	// auth or unauth rpc?
	AuthenticatedRPC bool `long:"authrpc" description:"Whether or not to use authenticated RPC"`

	// support lightning or not to support lightning?
	LightningSupport bool `long:"lightning" description:"Whether or not to support lightning on the exchange"`

	// database information
	DBUsername string `long:"dbuser" description:"database username"`
	DBPassword string `long:"dbpassword" description:"database password"`
	DBHost     string `long:"dbhost" description:"Host for the database connection"`
	DBPort     uint16 `long:"dbport" description:"Port for the database connection"`

	// Auction server options
	AuctionTime uint64 `long:"auctiontime" description:"Time it should take to generate a timelock puzzle protected order"`
}

var (
	defaultHomeDir = os.Getenv("HOME")

	// used as defaults before putting into parser
	defaultFredHomeDirName = defaultHomeDir + "/.opencx/fred/"
	defaultRpcport         = uint16(12345)
	defaultRpchost         = "localhost"
	defaultMaxPeers        = uint16(64)
	defaultMinPeerPort     = uint16(25565)
	defaultLithost         = "localhost"
	defaultLitport         = uint16(12346)

	// Yes we want to use noise-rpc
	defaultAuthenticatedRPC = true

	// Yes we want lightning
	defaultLightningSupport = true

	// default database stuff
	defaultDBUsername = "opencx"
	defaultDBPassword = "testpass"
	defaultDBHost     = "localhost"
	defaultDBPort     = uint16(3306)

	// default auction options
	defaultAuctionTime = uint64(30000)
)

// newConfigParser returns a new command line flags parser.
func newConfigParser(conf *fredConfig, options flags.Options) *flags.Parser {
	parser := flags.NewParser(conf, options)
	return parser
}

func main() {
	var err error

	conf := fredConfig{
		FredHomeDir:      defaultFredHomeDirName,
		Rpcport:          defaultRpcport,
		Rpchost:          defaultRpchost,
		MaxPeers:         defaultMaxPeers,
		MinPeerPort:      defaultMinPeerPort,
		Lithost:          defaultLithost,
		Litport:          defaultLitport,
		AuthenticatedRPC: defaultAuthenticatedRPC,
		LightningSupport: defaultLightningSupport,
		DBUsername:       defaultDBUsername,
		DBPassword:       defaultDBPassword,
		DBHost:           defaultDBHost,
		DBPort:           defaultDBPort,
		AuctionTime:      defaultAuctionTime,
	}

	// Check and load config params
	key := opencxSetup(&conf)

	var db *cxdbsql.DB
	if db, err = cxdbsql.CreateDBConnection(conf.DBUsername, conf.DBPassword, conf.DBHost, conf.DBPort); err != nil {
		logging.Fatalf("Error initializing Database: \n%s", err)
	}

	// Generate the coin list based on the parameters we know
	coinList := generateCoinList(&conf)

	// Setup DB Client
	if err = db.SetupClient(coinList); err != nil {
		log.Fatalf("Error setting up sql client: \n%s", err)
	}

	// Anyways, here's where we set the server
	var fredServer *cxauctionserver.OpencxAuctionServer
	if fredServer, err = cxauctionserver.InitServer(db, 100, conf.AuctionTime); err != nil {
		logging.Fatalf("Error initializing server: \n%s", err)
	}

	// Register RPC Commands and set server
	rpc1 := new(cxauctionrpc.OpencxAuctionRPC)
	rpc1.OffButton = make(chan bool, 1)
	rpc1.Server = fredServer

	// SIGINT and SIGTERM and SIGQUIT handler for CTRL-c, KILL, CTRL-/, etc.
	go func() {
		logging.Infof("Notifying signals")
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGQUIT)
		signal.Notify(sigs, syscall.SIGTERM)
		signal.Notify(sigs, syscall.SIGINT)
		for {
			signal := <-sigs
			logging.Infof("Received %s signal, Stopping server gracefully...", signal.String())

			// send off button to off button
			rpc1.OffButton <- true

			return
		}
	}()

	doneChan := make(chan bool, 1)
	if !conf.AuthenticatedRPC {
		// this tells us when the rpclisten is done
		logging.Infof(" === will start to listen on rpc ===")
		go cxauctionrpc.RPCListenAsync(doneChan, rpc1, conf.Rpchost, conf.Rpcport)
	} else {
		privkey, _ := koblitz.PrivKeyFromBytes(koblitz.S256(), key[:])
		// this tells us when the rpclisten is done
		logging.Infof(" === will start to listen on noise-rpc ===")
		go cxauctionrpc.NoiseListenAsync(doneChan, privkey, rpc1, conf.Rpchost, conf.Rpcport)
	}

	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()

	<-doneChan

	return
}
