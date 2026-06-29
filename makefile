list_schema:
	go tool iceberg --catalog hadoop --warehouse /tmp/iceberg-warehouse schema default.triples

list_files:
	go tool iceberg --catalog hadoop --warehouse /tmp/iceberg-warehouse files default.triples

copy_geoconnex_graph:
	gsutil -m cp -r gs://harvest-geoconnex-us/graphs/latest testdata/

install:
	go build -o ~/.local/bin

deadcode:
	deadcode ./...