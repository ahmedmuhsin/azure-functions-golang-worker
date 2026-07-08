module github.com/azure/azure-functions-golang-worker/samples/customhandler

go 1.24.0

require (
	github.com/azure/azure-functions-golang-worker/customhandler v0.0.0
	github.com/azure/azure-functions-golang-worker/sdk v0.0.0
)

replace (
	github.com/azure/azure-functions-golang-worker/customhandler => ../../customhandler
	github.com/azure/azure-functions-golang-worker/sdk => ../../sdk
)
