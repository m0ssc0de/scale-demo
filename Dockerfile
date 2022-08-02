FROM onfinality/go-dict:builder as builder
COPY ./ /data/src/
RUN go env -w CGO_CFLAGS="-I/rocksdb/include" && \
    go env -w CGO_LDFLAGS="-L/rocksdb -lrocksdb -lstdc++ -lm -lz -lbz2 -lsnappy -llz4 -lzstd" &&\
    cd /data/src && go build -ldflags="-extldflags=-static"

#FROM ubuntu
#FROM scratch
FROM alpine
RUN apk add bash jq
COPY --from=builder /data/src/config.json /
COPY --from=builder /data/src/go-dictionary /
COPY --from=builder /data/src/config.json /
COPY --from=builder /data/src/network/*.json /
