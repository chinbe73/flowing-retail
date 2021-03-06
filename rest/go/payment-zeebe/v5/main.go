package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"strings"

	"github.com/zeebe-io/zeebe/clients/go/entities"
	"github.com/zeebe-io/zeebe/clients/go/worker"
	"github.com/zeebe-io/zeebe/clients/go/zbc"
)

const (
	zeebeBrokerAddr = "0.0.0.0:26500"
	port            = "8100"
)

var (
	client zbc.ZBClient
)

func main() {
	startHTTPServer()
}

func startHTTPServer() {
	// setup REST endpoint (yay - this is not really REST - I know - but sufficient for this example)
	http.HandleFunc("/payment", handleHTTPRequest)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func init() {
	// connect to Zeebe Broker
	newClient, err := zbc.NewZBClient(&zbc.ZBClientConfig{
		GatewayAddress:         zeebeBrokerAddr,
		UsePlaintextConnection: true})
	if err != nil {
		log.Fatal(err)
	}
	client = newClient

	// register job handler for 'charge-credit-card' and subscribe
	client.NewJobWorker().JobType("charge-credit-card").Handler(handleChargeCreditCardJob).Open()
	client.NewJobWorker().JobType("deduct-customer-credit").Handler(handleDeductCustomerCredit).Open()

	//handleInterrupt(subscription1, subscription2)

	// deploy workflow model
	deployment, err := client.NewDeployWorkflowCommand().AddResourceFile("payment.bpmn").Send()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("deployed workflow model: ", deployment)
}

func handleHTTPRequest(w http.ResponseWriter, r *http.Request) {
	bodyBytes, _ := ioutil.ReadAll(r.Body)
	jsonStr := string(bodyBytes)
	fmt.Println("Retrieving payment request" + jsonStr)

	err := chargeCreditCard(jsonStr)
	if err != nil {
		w.WriteHeader(500)
	} else {
		w.WriteHeader(202)
	}
}

func chargeCreditCard(someDataAsJSON string) error {
	payload := make(map[string]interface{})
	json.Unmarshal([]byte(someDataAsJSON), &payload)
	request, err := client.NewCreateInstanceCommand().BPMNProcessId("paymentV5").LatestVersion().VariablesFromMap(payload)
	if err != nil {
		fmt.Println("Error: " + err.Error())
	}
	workflowInstance, err := request.Send()
	if err != nil {
		fmt.Println("Error: " + err.Error())
		return err
	}

	fmt.Println("Started: " + workflowInstance.String())
	return nil
}

func handleChargeCreditCardJob(client worker.JobClient, job entities.Job) {
	variables, err := job.GetVariablesAsMap()
	if err != nil {
		// failed to handle job as we require the variables
		failJob(client, job, err)
		return
	}
	jsonPayload, err := json.Marshal(variables)
	if err != nil {
		log.Fatal(err)
		failJob(client, job, err)
		return
	}

	_, err = doHTTPCall(string(jsonPayload))
	if err != nil {
		// couldn't do http call, fail job to trigger retry
		failJob(client, job, err)
	} else {
		// complete job after processing
		variables := make(map[string]interface{})
		variables["chargeSuccess"] = true
		completeJob(client, job, variables)
	}
}

func doHTTPCall(someDataAsJSON string) (resp *http.Response, err error) {
	fmt.Println("Doing http call", someDataAsJSON)
	return http.Post("http://localhost:8099/charge", "application/json", strings.NewReader(someDataAsJSON))
}

func handleDeductCustomerCredit(client worker.JobClient, job entities.Job) {
	// skip reading any data from the job as we don't care for the demo

	variables := make(map[string]interface{})
	variables["chargeSuccess"] = true
	// Hardcoded remaining amount, TODO: replace with randomized value
	if rand.Intn(10) > 3 {
		variables["remainingAmount"] = 5
		log.Println("[worker::deduct-customer-credit] Substracting from customer account - with remaining amount")
	} else {
		variables["remainingAmount"] = 0
		log.Println("[worker::deduct-customer-credit] Substracting from customer account - no remaining amount")
	}

	completeJob(client, job, variables)
}

func completeJob(client worker.JobClient, job entities.Job, variables map[string]interface{}) {

	request, err := client.NewCompleteJobCommand().JobKey(job.GetKey()).VariablesFromMap(variables)
	if err != nil {
		failJob(client, job, err)
		return
	}
	log.Println("Complete job", job, variables)
	request.Send()
}

func failJob(client worker.JobClient, job entities.Job, err error) {
	log.Println("Failed to complete job", job.GetKey(), err)
	if job.Retries > 1 {
		client.NewFailJobCommand().JobKey(job.GetKey()).Retries(job.Retries - 1).Send()
	} else {
		// if no more retries left, complete the task but mark it for being failed
		variables := make(map[string]interface{})
		variables["chargeSuccess"] = false
		completeJob(client, job, variables)
	}
}
