package main

import (
	"fmt"
	"github.com/spf13/viper"
	"github.com/streadway/amqp"
	"io/ioutil"
	"log"
	"strings"
	"time"
)

type rabbitConf struct {
	hostname          string
	port              int
	username          string
	password          string
	filename          string
	queriesExchange   string
	queriesQueue      string
	queriesRoutingKey string
}

type rabbitArtifacts struct {
	queriesExchangeName string
	queriesQueueName    string
}

type rabbitMqDestination struct {
	destination string
	routingKey  string
}

func main() {
	rabbitConfig := readRabbitConf()
	conn := connectRabbit(rabbitConfig)

	ch, err := conn.Channel()
	failOnError(err, "Failed to open a channel")

	content, err := ioutil.ReadFile(rabbitConfig.filename)
	failOnError(err, "failed to read data file")

	rabbitArtifacts := setupRabbitMqTopicsAndQueues(ch, rabbitConfig.queriesExchange, rabbitConfig.queriesQueue, rabbitConfig.queriesRoutingKey)

	msgs, consumeErr := ch.Consume(
		rabbitArtifacts.queriesQueueName,
		"",
		false,
		false,
		false,
		false,
		nil,
	)
	failOnError(consumeErr, "failed to consume messages from queue")

	forever := make(chan bool)
	answersToSend := make(chan rabbitMqDestination)

	log.Printf(" [*] Waiting for messages. To exit press CTRL+C")
	go func() {
		for msg := range msgs {
			rabbitMqDest, err := extractDestinationAndRoutingKeyFromReplyTo(msg.ReplyTo)
			logOnError(err, "failed to parse reply-to: %s")
			if err != nil {
				msg.Nack(false, false)
			} else {
				log.Println(fmt.Sprintf("received a query message and will send repsonse to %s", rabbitMqDest))
				answersToSend <- rabbitMqDest
				msg.Ack(false)
			}
		}
	}()

	go func(channel *amqp.Channel, body []byte) {

		for {
			rabbitDest := <-answersToSend
			sendErr := channel.Publish(rabbitDest.destination, rabbitDest.routingKey, false, false,
				amqp.Publishing{
					ContentType: "text/plain",
					Body:        body,
				})
			logOnError(sendErr, "failed to send reply message:")
		}

	}(ch, content)

	<-forever
	defer ch.Close()
	defer conn.Close()
}

func extractDestinationAndRoutingKeyFromReplyTo(replyTo string) (rabbitMqDestination, error) {
	if len(replyTo) == 0 {
		return rabbitMqDestination{"", ""}, fmt.Errorf("cannot create destination and/or routing key from empty reply-to")
	}

	if strings.Contains(replyTo, "/") {
		destinationAndRoutingKey := strings.Split(replyTo, "/")
		if len(destinationAndRoutingKey) != 2 {
			return rabbitMqDestination{"", ""}, fmt.Errorf("cannot create destination and/or routing key from reply-to with more than two slashes (/)")
		}
		return rabbitMqDestination{destinationAndRoutingKey[0], destinationAndRoutingKey[1]}, nil
	} else {
		return rabbitMqDestination{replyTo, ""}, nil
	}
}

func readRabbitConf() rabbitConf {
	viper.SetConfigFile("config.properties")
	viper.SetConfigType("properties")

	//TODO: build default values localhost:5672 no credentials
	confErr := viper.ReadInConfig()
	logOnError(confErr, "No configuration file loaded - using defaults {}")
	hostname := viper.GetString("rabbitmq.hostname")
	port := viper.GetInt("rabbitmq.port")
	username := viper.GetString("rabbitmq.username")
	password := viper.GetString("rabbitmq.password")
	filename := viper.GetString("filename")
	queriesExchange := viper.GetString("rabbitmq.queries.exchange")
	queriesQueue := viper.GetString("rabbitmq.queries.exchange")
	queriesRoutingKey := viper.GetString("rabbitmq.queries.routingkey")

	return rabbitConf{
		hostname: hostname,
		port: port,
		username: username,
		password: password,
		filename: filename,
		queriesExchange: queriesExchange,
		queriesQueue: queriesQueue,
		queriesRoutingKey: queriesRoutingKey,
	}
}

func connectRabbit(conf rabbitConf) *amqp.Connection {
	for {
		conn, err := amqp.Dial(fmt.Sprintf("amqp://%s:%s@%s:%d/", conf.username, conf.password, conf.hostname, conf.port))
		if err == nil && conn != nil {
			log.Println("connected to rabbitmq")
			return conn
		} else {
			logOnError(err, "failed to connect to rabbitmq will retry in ")
			time.Sleep(1000)
		}
	}
}

func setupRabbitMqTopicsAndQueues(channel *amqp.Channel, queriesExchangeName string, queriesQueueName string, queriesRoutingKey string) rabbitArtifacts {
	exchangeErr := channel.ExchangeDeclare(queriesExchangeName, "topic", true, false, false, false, nil)
	failOnError(exchangeErr, "failed to declare queries exchange")

	_, queriesErr := channel.QueueDeclare(
		queriesQueueName,
		true,
		false,
		false,
		false,
		nil,
	)
	failOnError(queriesErr, "Failed to declare queries queue")

	//TODO make configurable by users input data
	bindErr := channel.QueueBind(queriesQueueName, queriesRoutingKey, queriesExchangeName, false, nil)
	failOnError(bindErr, "Failed to bind queries queue to topic exchange")

	log.Println(fmt.Sprintf("created topics and queues %s, %s", queriesQueueName, queriesExchangeName))

	return rabbitArtifacts{queriesExchangeName: queriesExchangeName, queriesQueueName: queriesQueueName}
}

func failOnError(err error, msg string) {
	if err != nil {
		log.Fatalf("%s: %s", msg, err)
		panic(fmt.Sprintf("%s: %s", msg, err))
	}
}
func logOnError(err error, msg string) {
	if err != nil {
		log.Printf("%s: %s\n", msg, err)
	}
}
