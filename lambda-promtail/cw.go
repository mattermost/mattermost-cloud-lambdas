package main

import (
	"context"
	"fmt"
	log "github.com/sirupsen/logrus"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/grafana/loki/pkg/logproto"
	"github.com/prometheus/common/model"
)

func parseCWEvent(ctx context.Context, b *batch, ev *events.CloudwatchLogsEvent) error {
	data, err := ev.AWSLogs.Parse()
	if err != nil {
		fmt.Println("error parsing log event: ", err)
		return err
	}

	for _, event := range data.LogEvents {
		labels := model.LabelSet{
			model.LabelName("__aws_cloudwatch_log_group"): model.LabelValue(data.LogGroup),
			model.LabelName("__aws_cloudwatch_owner"):     model.LabelValue(data.Owner),
		}

		if keepStream {
			labels[model.LabelName("__aws_cloudwatch_log_stream")] = model.LabelValue(data.LogStream)
		}
		res := strings.Contains(data.LogGroup, "slowquery")
		if includeMessageAsLabel && res {
			labels[model.LabelName("__aws_cloudwatch_message")] = model.LabelValue(event.Message)
		}
		labels = applyExtraLabels(labels)
		timestamp := time.UnixMilli(event.Timestamp)

		err := b.add(ctx, entry{labels, logproto.Entry{
			Line:      event.Message,
			Timestamp: timestamp,
		}})
		if err != nil {
			log.WithError(err)
		}
	}

	return nil
}

func processCWEvent(ctx context.Context, ev *events.CloudwatchLogsEvent) error {
	batch, _ := newBatch(ctx)

	err := parseCWEvent(ctx, batch, ev)
	if err != nil {
		return err
	}

	err = sendToPromtail(ctx, batch)
	if err != nil {
		return err
	}
	return nil
}
