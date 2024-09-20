package main

import (
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"runtime"
	"runtime/pprof"
	"sync"
	"time"

	dnstap "github.com/dnstap/golang-dnstap"
	"github.com/miekg/dns"
	"google.golang.org/protobuf/proto"
)

func main() {
	var cpuprofile = flag.String("cpuprofile", "", "write cpu profile to `file`")
	var uFlag = flag.String("u", "/tmp/dtbench.sock", "unix socket path to write to")
	var nFlag = flag.Int("n", 10, "number of packets to send")
	var gFlag = flag.Bool("g", false, "if we split up work into goroutines or not")
	var rFlag = flag.Bool("r", false, "if we send a response packet (by default it is a request)")
	var qFlag = flag.String("q", "example.com.", "qname to query for, terminating '.' is added if needed")
	flag.Parse()

	naddr, err := net.ResolveUnixAddr("unix", *uFlag)
	if err != nil {
		log.Fatal(err)
	}

	dnstapOutput, err := dnstap.NewFrameStreamSockOutput(naddr)
	if err != nil {
		log.Fatal(err)
	}

	logger := slog.Default()

	slog.SetDefault(logger)

	dnstapLogger := log.Default()

	dnstapOutput.SetTimeout(time.Second * 3)
	dnstapOutput.SetLogger(dnstapLogger)

	outputChannel := dnstapOutput.GetOutputChannel()

	go dnstapOutput.RunOutputLoop()

	b := createDnstapPacket(*rFlag, *qFlag)

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal("could not create CPU profile: ", err)
		}
		defer func() {
			err = f.Close()
			if err != nil {
				log.Fatalf("unable to close cpuprofile file: %s", err)
			}
		}()
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatal("could not start CPU profile: ", err)
		}
		defer pprof.StopCPUProfile()
	}

	startTime := time.Now()
	if *gFlag {
		fmt.Printf("Starting multiple senders\n")

		baseAttemptsPerWorker := *nFlag / runtime.GOMAXPROCS(0)
		remainderAttempts := *nFlag % runtime.GOMAXPROCS(0)

		wg := &sync.WaitGroup{}
		for procNum := 1; procNum <= runtime.GOMAXPROCS(0); procNum++ {
			attempts := baseAttemptsPerWorker
			if procNum <= remainderAttempts {
				attempts++
			}
			if attempts > 0 {
				fmt.Printf("worker %d: %d attempts\n", procNum, attempts)
				wg.Add(1)
				go senderFunc(wg, attempts, b, outputChannel)
			} else {
				break
			}
		}
		wg.Wait()
	} else {
		fmt.Printf("Starting single sender\n")
		for attempt := 1; attempt <= *nFlag; attempt++ {
			outputChannel <- b
		}
	}

	dnstapOutput.Close()
	runDuration := time.Since(startTime)

	fmt.Printf("Sent %d packets, took %s (%f dnstap/sec)\n", *nFlag, runDuration, float64(*nFlag)/runDuration.Seconds())
}

func createDnstapPacket(response bool, qname string) []byte {
	dnstapType := dnstap.Dnstap_MESSAGE

	queryAddr, err := netip.ParseAddr("198.51.100.2")
	if err != nil {
		log.Fatal(err)
	}

	responseAddr, err := netip.ParseAddr("198.51.100.3")
	if err != nil {
		log.Fatal(err)
	}

	socketFamily := dnstap.SocketFamily_INET
	socketProtocol := dnstap.SocketProtocol_UDP
	queryPort := uint32(31337)
	responsePort := uint32(53)
	queryTime := time.Now()
	queryTimeSec := uint64(queryTime.Unix())
	queryTimeNsec := uint32(queryTime.Nanosecond())
	responseTime := time.Now()
	responseTimeSec := uint64(responseTime.Unix())
	responseTimeNsec := uint32(responseTime.Nanosecond())

	queryMsg := new(dns.Msg)
	queryMsg.SetQuestion(dns.Fqdn(qname), dns.TypeA)
	queryBytes, err := queryMsg.Pack()
	if err != nil {
		log.Fatal(err)
	}

	var responseBytes []byte
	if response {
		responseMsg := new(dns.Msg)
		responseMsg.SetReply(queryMsg)
		aRR, err := dns.NewRR("example.com. 3600 IN A 198.51.100.4")
		if err != nil {
			log.Fatal(err)
		}
		responseMsg.Answer = []dns.RR{aRR}

		responseBytes, err = queryMsg.Pack()
		if err != nil {
			log.Fatal(err)
		}
	}

	dnstapMessage := dnstap.Message{
		SocketFamily:     &socketFamily,
		SocketProtocol:   &socketProtocol,
		QueryAddress:     queryAddr.AsSlice(),
		ResponseAddress:  responseAddr.AsSlice(),
		QueryPort:        &queryPort,
		ResponsePort:     &responsePort,
		QueryTimeSec:     &queryTimeSec,
		QueryTimeNsec:    &queryTimeNsec,
		ResponseTimeSec:  &responseTimeSec,
		ResponseTimeNsec: &responseTimeNsec,
	}

	if response {
		messageTypeClientResponse := dnstap.Message_CLIENT_RESPONSE
		dnstapMessage.Type = &messageTypeClientResponse
		dnstapMessage.ResponseMessage = responseBytes
	} else {
		messageTypeClientQuery := dnstap.Message_CLIENT_QUERY
		dnstapMessage.Type = &messageTypeClientQuery
		dnstapMessage.QueryMessage = queryBytes
	}

	dt := &dnstap.Dnstap{
		Type:    &dnstapType,
		Message: &dnstapMessage,
	}

	b, err := proto.Marshal(dt)
	if err != nil {
		log.Fatal(err)
	}

	return b
}

func senderFunc(wg *sync.WaitGroup, attempts int, b []byte, outputChannel chan []byte) {
	defer wg.Done()
	for attempt := 1; attempt <= attempts; attempt++ {
		outputChannel <- b
	}
}
