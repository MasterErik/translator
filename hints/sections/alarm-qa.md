# Alarm Platform Q&A

<!-- fact
{
  "id": "alarm-qa-01",
  "section": "alarm-qa",
  "title": "What is the main business goal of the platform?",
  "keywords": [
    "business goal",
    "medication orders",
    "heterogeneous sources",
    "canonical format",
    "data loss"
  ],
  "aliases": [
    "main business objective",
    "why the platform exists"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
Collecting medication orders from over 1,000 heterogeneous external sources (pharmacies, distributors) without data loss and converting them into a unified canonical format.

<!-- fact
{
  "id": "alarm-qa-02",
  "section": "alarm-qa",
  "title": "What is the main architectural problem being solved?",
  "keywords": [
    "architectural problem",
    "third-party instability",
    "legacy protocols",
    "partner contract violations",
    "internal infrastructure isolation"
  ],
  "aliases": [
    "what architecture problem is solved",
    "why isolate internal systems"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
Isolating the internal infrastructure from the extreme instability of third-party systems (legacy protocols, unpredictable communication channels, and partner contract violations).

<!-- fact
{
  "id": "alarm-qa-03",
  "section": "alarm-qa",
  "title": "Why was a microservice approach (Alarm + Download) chosen over a monolithic poller?",
  "keywords": [
    "microservice approach",
    "alarm and download",
    "monolithic poller",
    "stateless workers",
    "independent scaling",
    "polling schedule"
  ],
  "aliases": [
    "why two microservices instead of one poller",
    "services split rationale"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
To ensure the stateless nature of the download workers. This allows for independent scaling of the download nodes pool and seamless restarts without losing the polling schedule, which is managed by a separate service.

<!-- fact
{
  "id": "alarm-qa-04",
  "section": "alarm-qa",
  "title": "What acts as the source of truth for integration settings?",
  "keywords": [
    "source of truth",
    "integration settings",
    "postgresql",
    "metadata",
    "ftp credentials",
    "polling frequency"
  ],
  "aliases": [
    "where integration config is stored",
    "configuration database"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
A PostgreSQL database that stores metadata: FTP/Mail addresses, credentials, polling frequency, and file parsing parameters.

<!-- fact
{
  "id": "alarm-qa-05",
  "section": "alarm-qa",
  "title": "What incoming data protocols are supported?",
  "keywords": [
    "incoming data protocols",
    "ftp",
    "sftp",
    "email"
  ],
  "aliases": [
    "which transfer protocols are supported",
    "how files arrive"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
The system supports receiving files via FTP, SFTP, and Email.

<!-- fact
{
  "id": "alarm-qa-06",
  "section": "alarm-qa",
  "title": "How is file deletion handled on the partner servers?",
  "keywords": [
    "file deletion",
    "partner servers",
    "source side deletion",
    "post-processing cleanup"
  ],
  "aliases": [
    "is the file removed from the partner server",
    "delete after download"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
After successful downloading and processing, the platform executes a deletion command on the source side (if the partner's server supports it).

<!-- fact
{
  "id": "alarm-qa-07",
  "section": "alarm-qa",
  "title": "How does the Alarm service work?",
  "keywords": [
    "alarm service",
    "control ticks",
    "tick events",
    "kafka",
    "polling schedule"
  ],
  "aliases": [
    "how the alarm tick generator works",
    "tick production flow"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
It cyclically polls the integration schedule and generates control ticks (Tick Events), sending them to Kafka for the workers. Each message contains the full context needed for the connection.

<!-- fact
{
  "id": "alarm-qa-08",
  "section": "alarm-qa",
  "title": "How is High Availability (HA) ensured for the Alarm tick generator?",
  "keywords": [
    "high availability",
    "kafka partitions",
    "ftp host",
    "partitioning key",
    "sequential processing",
    "worker pods"
  ],
  "aliases": [
    "how alarm ha is achieved",
    "tick distribution guarantees"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
Generated ticks are efficiently distributed across Kafka partitions, with the `ftp host` acting as the partitioning key. This guarantees sequential processing of ticks for a single host without collisions between worker pods.

<!-- fact
{
  "id": "alarm-qa-09",
  "section": "alarm-qa",
  "title": "How are dynamic configuration updates (e.g., adding new hosts) implemented?",
  "keywords": [
    "dynamic configuration updates",
    "configuration microservice",
    "broadcast",
    "new hosts",
    "state comparison"
  ],
  "aliases": [
    "how new hosts are added at runtime",
    "config propagation"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
The logic is extracted into a separate configuration microservice. It periodically broadcasts the current configuration; the `Alarm` service compares it with its own state and applies updates.

<!-- fact
{
  "id": "alarm-qa-10",
  "section": "alarm-qa",
  "title": "How does Alarm know when to send the next tick?",
  "keywords": [
    "next tick",
    "last packet sent",
    "background thread",
    "polling interval"
  ],
  "aliases": [
    "how alarm schedules the next tick",
    "interval enforcement"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
The service tracks the time of the last packet sent for each host. A separate background thread iterates through the actual list and generates events strictly according to the defined interval.

<!-- fact
{
  "id": "alarm-qa-11",
  "section": "alarm-qa",
  "title": "Does the Alarm topic contain business data?",
  "keywords": [
    "alarm topic",
    "technical data",
    "tick metadata",
    "recreate topic"
  ],
  "aliases": [
    "does the alarm topic hold business data",
    "can alarm topic be cleared"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
No, the `Alarm` topic is purely technical. It only transmits tick metadata, so it can be painlessly cleared or recreated.

<!-- fact
{
  "id": "alarm-qa-12",
  "section": "alarm-qa",
  "title": "Is the system protected against duplicate ticks?",
  "keywords": [
    "duplicate ticks",
    "routing key",
    "drop-on-busy",
    "kafka partition"
  ],
  "aliases": [
    "is duplication of ticks protected against",
    "tick duplication safety"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
Yes, by using the `ftp host` as the Kafka routing key, even if there is a brief duplication of a tick from `Alarm`, the worker will correctly process or drop the duplicate thanks to the `Drop-on-Busy` strategy.

<!-- fact
{
  "id": "alarm-qa-13",
  "section": "alarm-qa",
  "title": "Can different polling frequencies be set for different partners?",
  "keywords": [
    "polling frequency",
    "sla",
    "postgresql configuration",
    "partner-specific intervals"
  ],
  "aliases": [
    "different schedules per partner",
    "configurable poll interval"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
Yes, the polling frequency is dynamic and configured in PostgreSQL (from 2 to 30 minutes) depending on the partner's SLAs.

<!-- fact
{
  "id": "alarm-qa-14",
  "section": "alarm-qa",
  "title": "How is the isolation of different FTP connections implemented?",
  "keywords": [
    "ftp connection isolation",
    "prototype beans",
    "spring",
    "connection state"
  ],
  "aliases": [
    "how connections are isolated per tick",
    "bean instance per connection"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
Via the `Prototype Beans` pattern in Spring. For each incoming tick from Kafka, a separate bean instance is created or reused, encapsulating the state of that specific connection.

<!-- fact
{
  "id": "alarm-qa-15",
  "section": "alarm-qa",
  "title": "What is the limit of concurrently existing Prototype Beans?",
  "keywords": [
    "prototype bean limit",
    "1000 instances",
    "concurrent beans",
    "hosts upper bound"
  ],
  "aliases": [
    "how many prototype beans can exist",
    "concurrency cap"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
A maximum of 1,000 instances, which corresponds to the upper limit of the polled hosts.

<!-- fact
{
  "id": "alarm-qa-16",
  "section": "alarm-qa",
  "title": "What is the Drop-on-Busy strategy?",
  "keywords": [
    "drop-on-busy",
    "busy worker",
    "tick dropped",
    "download all files"
  ],
  "aliases": [
    "what happens when the worker is busy",
    "tick skipping strategy"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
If the target `Prototype Bean` is busy downloading files, and a new tick arrives from Kafka for the same host, the message is dropped. Skipping is safe because the tick's job is to download all currently available files.

<!-- fact
{
  "id": "alarm-qa-17",
  "section": "alarm-qa",
  "title": "Does a busy worker delay the processing of other hosts in the same partition?",
  "keywords": [
    "partition blocking",
    "consumer thread",
    "busy state check",
    "instant drop"
  ],
  "aliases": [
    "does one host block the partition",
    "partition throughput"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
No. The consumer thread reads the message and checks the target bean's busy state. If busy, the consumer instantly drops the message and moves to the next one, without blocking the partition.

<!-- fact
{
  "id": "alarm-qa-18",
  "section": "alarm-qa",
  "title": "How is the issue of memory and socket resource leaks resolved in the bean pool?",
  "keywords": [
    "resource leaks",
    "reaper thread",
    "inactivity timeout",
    "bean destruction",
    "socket close"
  ],
  "aliases": [
    "how memory and socket leaks are prevented",
    "idle bean cleanup"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
A background `Reaper Thread` is implemented. It tracks the inactivity time of the beans. If there is no activity for 15 minutes, the thread forcibly destroys the bean instance and closes the sockets.

<!-- fact
{
  "id": "alarm-qa-19",
  "section": "alarm-qa",
  "title": "How does the system handle hanging FTP sessions (Zombie Sockets) that the server doesn't close?",
  "keywords": [
    "zombie sockets",
    "hanging ftp sessions",
    "blocking sockets",
    "architectural limitation",
    "restart cleanup"
  ],
  "aliases": [
    "how stuck ftp connections are handled",
    "unclosable sockets"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
When using blocking sockets within standard network libraries, it is fundamentally impossible to programmatically sever a hanging connection. The platform accepts this as an architectural limitation, and hanging threads are cleared during scheduled deployments/restarts.

<!-- fact
{
  "id": "alarm-qa-20",
  "section": "alarm-qa",
  "title": "What happens to open connections during a Kafka rebalance or deployment?",
  "keywords": [
    "kafka rebalance",
    "deployment",
    "application restart",
    "connection termination",
    "operating system"
  ],
  "aliases": [
    "what happens to connections during rebalance",
    "restart behavior"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
A rebalance during deployment implies a full application restart. At this moment, all running `Prototype Beans` and their network connections are properly terminated by the operating system.

<!-- fact
{
  "id": "alarm-qa-21",
  "section": "alarm-qa",
  "title": "Does a file download restart if a network failure occurs at 99%?",
  "keywords": [
    "partial download",
    "network failure",
    "temporary directory",
    "transaction rollback",
    "next tick retry"
  ],
  "aliases": [
    "is a failed download resumed",
    "99% failure behavior"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
The file is downloaded to a temporary directory/memory. Upon failure, it is discarded, and the transaction is not completed. The download will start over at the next Alarm tick.

<!-- fact
{
  "id": "alarm-qa-22",
  "section": "alarm-qa",
  "title": "What acts as the partitioning key in Kafka topics?",
  "keywords": [
    "partitioning key",
    "kafka topics",
    "ftp server identifier"
  ],
  "aliases": [
    "what kafka keys messages by",
    "partition key choice"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
The identifier (name) of the FTP server.

<!-- fact
{
  "id": "alarm-qa-23",
  "section": "alarm-qa",
  "title": "Why is it important to partition specifically by host?",
  "keywords": [
    "host partitioning",
    "worker pod affinity",
    "ip ban",
    "weak legacy server",
    "connection attempts"
  ],
  "aliases": [
    "why partition by host",
    "avoiding parallel connections to one server"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
This guarantees that all tasks for the same legacy server will always hit the same worker pod, preventing simultaneous multiple connection attempts to a weak server, which could lead to an IP ban.

<!-- fact
{
  "id": "alarm-qa-24",
  "section": "alarm-qa",
  "title": "How does the processing pipeline scale?",
  "keywords": [
    "horizontal scaling",
    "download replicas",
    "partitions increase",
    "processing pipeline"
  ],
  "aliases": [
    "how the pipeline scales out",
    "scaling strategy"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
By increasing the number of replicas for the `Download` microservice and proportionally increasing the number of partitions in the Kafka topics.

<!-- fact
{
  "id": "alarm-qa-25",
  "section": "alarm-qa",
  "title": "What delivery guarantees does the Kafka configuration provide?",
  "keywords": [
    "delivery guarantees",
    "at-least-once",
    "manual offset commit",
    "s3",
    "redis"
  ],
  "aliases": [
    "kafka delivery semantics",
    "when offsets are committed"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
`At-least-once`. Manual Offset Commit ensures that the offset is shifted only after the data is fully written to S3 and Redis.

<!-- fact
{
  "id": "alarm-qa-26",
  "section": "alarm-qa",
  "title": "Is Log Compaction used for topics?",
  "keywords": [
    "log compaction",
    "topic retention",
    "alarm topic",
    "outbox topics"
  ],
  "aliases": [
    "is log compaction used",
    "compaction vs retention"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
In the Alarm topic, `Log Compaction` is unnecessary (it is technical and ephemeral). In Outbox topics, compaction can be configured, but standard retention is preferred.

<!-- fact
{
  "id": "alarm-qa-27",
  "section": "alarm-qa",
  "title": "Why was two-stage deduplication required?",
  "keywords": [
    "two-stage deduplication",
    "network traffic savings",
    "processing time",
    "unchanged files"
  ],
  "aliases": [
    "why two dedup stages",
    "purpose of deduplication"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
To radically save network traffic and processing time by skipping the download of files that haven't changed.

<!-- fact
{
  "id": "alarm-qa-28",
  "section": "alarm-qa",
  "title": "How does the first deduplication stage (MDTM) work?",
  "keywords": [
    "mdtm",
    "first deduplication stage",
    "sha-256",
    "file modification time",
    "metadata hash"
  ],
  "aliases": [
    "how mdtm deduplication works",
    "hash without download"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
If the FTP server supports the `MDTM` (Modification Time) command, a hash is generated on the fly from the metadata: `SHA-256(FileName + LastModifiedDate)` without downloading the file body.

<!-- fact
{
  "id": "alarm-qa-29",
  "section": "alarm-qa",
  "title": "What if the server does not support MDTM?",
  "keywords": [
    "mdtm unsupported",
    "second deduplication stage",
    "content hash",
    "full download",
    "binary body"
  ],
  "aliases": [
    "what if the server lacks mdtm",
    "fallback dedup stage"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
The second stage is engaged: the file is downloaded completely, and then a Content Hash is calculated from the binary file body.

<!-- fact
{
  "id": "alarm-qa-30",
  "section": "alarm-qa",
  "title": "How is file uniqueness verified?",
  "keywords": [
    "redis key",
    "file uniqueness",
    "host id",
    "file name",
    "file hash",
    "atomic check"
  ],
  "aliases": [
    "how duplicates are detected in redis",
    "uniqueness check"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
The calculated hash serves as part of the Redis key (`[Host_ID + File_Name + File_Hash]`). An atomic request to Redis determines if a duplicate exists.

<!-- fact
{
  "id": "alarm-qa-31",
  "section": "alarm-qa",
  "title": "What happens if Redis identifies the file as a duplicate?",
  "keywords": [
    "duplicate file",
    "file ignored",
    "tick completion",
    "resource release"
  ],
  "aliases": [
    "what happens on a redis duplicate hit",
    "duplicate handling"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
The file is ignored, the tick is completed (or moves to the next file), and resources are released.

<!-- fact
{
  "id": "alarm-qa-32",
  "section": "alarm-qa",
  "title": "What is the TTL (Time-To-Live) of deduplication keys in Redis?",
  "keywords": [
    "redis ttl",
    "time-to-live",
    "deduplication keys",
    "24 hours"
  ],
  "aliases": [
    "how long dedup keys live",
    "expiration of redis keys"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
1 day (24 hours).

<!-- fact
{
  "id": "alarm-qa-33",
  "section": "alarm-qa",
  "title": "How does the system handle Read-Only FTP servers that prohibit file deletion?",
  "keywords": [
    "read-only ftp",
    "deletion prohibited",
    "key still stored",
    "ttl window",
    "filtering"
  ],
  "aliases": [
    "servers that forbid deletion",
    "read-only partner handling"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
Even if the file remains on the FTP, its key is placed in Redis during the first processing. For the next 24 hours (while the TTL is alive), the file will be instantly filtered out during the key matching stage.

<!-- fact
{
  "id": "alarm-qa-34",
  "section": "alarm-qa",
  "title": "Is there a risk of Redis RAM overflowing and keys being lost due to Eviction Policy?",
  "keywords": [
    "redis memory",
    "eviction policy",
    "sizing",
    "key retention guarantee"
  ],
  "aliases": [
    "can redis evict dedup keys",
    "ram overflow risk"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
This risk is eliminated at the sizing level: enough memory is allocated to guarantee the storage of all file keys with a 24-hour TTL.

<!-- fact
{
  "id": "alarm-qa-35",
  "section": "alarm-qa",
  "title": "Why use Redis for deduplication instead of PostgreSQL?",
  "keywords": [
    "redis vs postgresql",
    "in-memory state",
    "thousands of checks per second",
    "automatic cleanup",
    "ephemeral state"
  ],
  "aliases": [
    "why redis instead of a database",
    "dedup store choice"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
Deduplication operations require thousands of checks per second. The in-memory nature of Redis and automatic key cleanup via TTL make it ideal for ephemeral processing state storage.

<!-- fact
{
  "id": "alarm-qa-36",
  "section": "alarm-qa",
  "title": "What is the core of the Dual-Write problem in this architecture?",
  "keywords": [
    "dual-write problem",
    "s3 write",
    "redis commit",
    "kafka publish",
    "atomicity",
    "network glitch"
  ],
  "aliases": [
    "what the dual-write issue is",
    "three-way atomic write"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
The need to atomically write a binary file to Amazon S3, commit the key in Redis, and send a message to the next Kafka topic, eliminating the scenario where one action succeeds but another fails due to a network glitch.

<!-- fact
{
  "id": "alarm-qa-37",
  "section": "alarm-qa",
  "title": "What is the algorithm of the Outbox Pattern here?",
  "keywords": [
    "outbox pattern algorithm",
    "pending event",
    "complete event",
    "redis key commit",
    "kafka offset commit"
  ],
  "aliases": [
    "how the transactional outbox works",
    "pending and complete flow"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
The file is written to S3, and a PENDING event is sent to Kafka. The consumer waits for a COMPLETE event. Upon receiving it, the key is committed in Redis, and the Kafka offset is committed.

<!-- fact
{
  "id": "alarm-qa-38",
  "section": "alarm-qa",
  "title": "How is a desynchronization of PENDING and COMPLETE statuses handled?",
  "keywords": [
    "pending complete desynchronization",
    "single consumer",
    "failure events",
    "hashmap lookup",
    "o(1)"
  ],
  "aliases": [
    "how status mismatches are handled",
    "outbox consumer design"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
There is always exactly one consumer dedicated to this task. Failure events are negligibly rare, and lookups in the local `HashMap` take O(1) time.

<!-- fact
{
  "id": "alarm-qa-39",
  "section": "alarm-qa",
  "title": "Why was an In-Memory HashMap chosen for the wait state instead of Kafka Streams (State Store)?",
  "keywords": [
    "in-memory hashmap",
    "kafka streams state store",
    "kiss principle",
    "thread-safe collection",
    "minimal load"
  ],
  "aliases": [
    "why a hashmap instead of kafka streams",
    "state store decision"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
To adhere to the KISS (Keep It Simple, Stupid) principle. The load is minimal, so there's no reason to complicate the system with heavy tools like Kafka Streams when a standard thread-safe collection solves the problem.

<!-- fact
{
  "id": "alarm-qa-40",
  "section": "alarm-qa",
  "title": "Why couldn't TTL records in Redis be used instead of the Outbox Consumer?",
  "keywords": [
    "redis ttl records",
    "outbox consumer",
    "key destruction",
    "inconsistency",
    "manual commit"
  ],
  "aliases": [
    "why not use redis ttl instead of outbox",
    "consistency binding"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
If Redis were used, a key could be destroyed (by TTL or memory failure) while the uncommitted record in Kafka remained, leading to inconsistency. The `HashMap` with a manual Kafka commit ensures a strict binding.

<!-- fact
{
  "id": "alarm-qa-41",
  "section": "alarm-qa",
  "title": "What is the wait timeout in the HashMap for the COMPLETE status?",
  "keywords": [
    "wait timeout",
    "complete status",
    "10 seconds",
    "transaction rollback",
    "kafka offset"
  ],
  "aliases": [
    "how long the hashmap waits",
    "timeout behavior"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
The worker waits up to 10 seconds for the COMPLETE event. If a timeout occurs, the transaction rolls back, and Kafka retains the old offset.

<!-- fact
{
  "id": "alarm-qa-42",
  "section": "alarm-qa",
  "title": "What happens if the worker crashes in the middle of the 10-second wait window?",
  "keywords": [
    "worker crash",
    "wait window",
    "offset redelivery",
    "re-execution of verification"
  ],
  "aliases": [
    "what happens if the worker dies mid-wait",
    "crash during wait"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
The Kafka offset won't shift. After the pod restarts, Kafka will redeliver the event with the old offset, and the Consumer will re-execute the verification.

<!-- fact
{
  "id": "alarm-qa-43",
  "section": "alarm-qa",
  "title": "How is the problem of \"Orphaned\" files in S3 (caused by service crashes) solved?",
  "keywords": [
    "orphaned files",
    "s3 lifecycle policy",
    "15-day ttl",
    "automatic purge",
    "crash leftovers"
  ],
  "aliases": [
    "how orphaned s3 objects are cleaned",
    "s3 ttl policy"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
An automatic `S3 Lifecycle Policy` with a 15-day TTL is configured for the bucket. Unlinked objects are automatically purged by the storage.

<!-- fact
{
  "id": "alarm-qa-44",
  "section": "alarm-qa",
  "title": "How does monitoring distinguish between false alarms (partner didn't upload files) and real pipeline failures?",
  "keywords": [
    "monitoring",
    "false alarms",
    "grafana",
    "successful packet processing",
    "zero results",
    "pipeline health"
  ],
  "aliases": [
    "distinguishing partner silence from failure",
    "observability of the pipeline"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
Grafana displays a metric for successful `Alarm` packet processing. This clearly shows that the pipeline is functioning normally and polling hosts, even if there are no new files (zero results).

<!-- fact
{
  "id": "alarm-qa-45",
  "section": "alarm-qa",
  "title": "Which metric signals the need to change the schedule in Alarm?",
  "keywords": [
    "tick drop rate",
    "metric",
    "busy workers",
    "scaling decision",
    "schedule change"
  ],
  "aliases": [
    "which metric triggers a schedule change",
    "drop rate signal"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
`Tick Drop Rate`. If it is high, it means the workers are constantly busy and dropping new ticks, which requires either scaling the workers or reducing the polling frequency for that specific host.

<!-- fact
{
  "id": "alarm-qa-46",
  "section": "alarm-qa",
  "title": "How is the stability of partner servers tracked?",
  "keywords": [
    "connection error rate",
    "partner server stability",
    "timeouts",
    "connection refusals",
    "availability"
  ],
  "aliases": [
    "how partner stability is measured",
    "external server errors"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
Via the `Connection Error Rate` metric, which visualizes the availability of external servers across each of the 1000+ partners (a counter of timeouts and connection refusals).

<!-- fact
{
  "id": "alarm-qa-47",
  "section": "alarm-qa",
  "title": "Does an FTP connection drop lead to a worker crash?",
  "keywords": [
    "ftp connection drop",
    "worker crash",
    "prometheus",
    "error logging",
    "standby mode"
  ],
  "aliases": [
    "does a connection failure kill the worker",
    "error resilience"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
No, the error context is logged and sent to Prometheus. The worker gracefully completes the tick without crashing and enters standby mode.

<!-- fact
{
  "id": "alarm-qa-48",
  "section": "alarm-qa",
  "title": "What does the Resource Metrics dashboard track?",
  "keywords": [
    "resource metrics dashboard",
    "prototype beans count",
    "reaper thread statistics",
    "inactive sessions"
  ],
  "aliases": [
    "what the resource dashboard shows",
    "bean pool telemetry"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
It monitors the current number of running `Prototype Beans` and gathers statistics on the `Reaper Thread` operations (the number of cleaned-up inactive sessions).

<!-- fact
{
  "id": "alarm-qa-49",
  "section": "alarm-qa",
  "title": "How is the appearance of invalid files (corrupted archives, wrong format) tracked?",
  "keywords": [
    "invalid files",
    "parse error metric",
    "failed status",
    "dead letter queue",
    "canonicalization error",
    "manual retry"
  ],
  "aliases": [
    "how corrupted files are tracked",
    "parse failure handling"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
Upon a canonization/parsing error, a `Parse Error` metric is generated, the file is marked as `FAILED` (or sent to a Dead Letter Queue - DLQ), and the key is not recorded as successful, leaving room for manual retry.

<!-- fact
{
  "id": "alarm-qa-50",
  "section": "alarm-qa",
  "title": "Is manual intervention required if the PostgreSQL database temporarily fails?",
  "keywords": [
    "postgresql failure",
    "manual intervention",
    "alarm schedule cache",
    "download independence",
    "automatic recovery"
  ],
  "aliases": [
    "what if the database goes down",
    "db outage resilience"
  ],
  "stack": [
    "java"
  ],
  "weight": "primary"
}
-->
No. The Alarm service will cache the last known schedule and continue generating ticks until the DB recovers. The Download microservice has no direct dependencies on PostgreSQL.
