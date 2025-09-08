1. Контрольная плоскость (Control Plane)
   1.1. Членство и обнаружение

IMembershipService

Ответственность: учёт живых узлов, join/leave, метаданные (ресурсы, регионы).

Ключевые методы: join(nodeInfo), leave(nodeId), nodes(), subscribeEvents(cb).

IHeartbeat/IFailureDetector

Ответственность: heartbeat, подозрение на падение, время реакции/таймауты.

Методы: start(nodeId), onBeat(nodeId), suspect(nodeId).

1.2. Топология и балансировка

ITopologyProvider

Ответственность: знание «кто где», гео/зональность, линки (P2P, брокер).

Методы: getRoute(targetNodeId), nearest(criteria).

ILoadBalancer / IPlacementPolicy

Ответственность: выбор исполнителя (по ресурсам, гео, латентности, приоритетам).

Методы: selectExecutor(taskMeta, candidates), rank(nodes, policy).

1.3. Консенсус и согласованное состояние

IConsensusModule (Raft/Paxos/BFT-адаптер)

Ответственность: согласование событий (создание задач, назначения, завершения).

Методы: appendLog(entry) -> index, read(index), barrier().

IStateStore (KV/Log)

Ответственность: хранение журнала задач, снапшоты.

Методы: put(key,val), get(key), scan(prefix), snapshot().

2. Данные и транспорт (Data Plane)
   2.1. Транспорт (канал доставки)

ITransportProvider

Ответственность: доставить сообщение узлу/группе (inproc, shm, TCP, gRPC, Redis, ZeroMQ…).

Методы: send(msg, nodeId), broadcast(msg, group), subscribe(handler).

Варианты: InProcTransport, ShmTransport, TcpTransport, GrpcTransport, RedisTransport, ZmqTransport.

2.2. Протокол/кодеки

IProtocolCodec

Ответственность: сериализация/десериализация сообщений (Task/Result/Control).

Методы: encode(Task) -> bytes, decode(bytes) -> Message.

Варианты: ProtobufCodec, JsonCodec, CapnProtoCodec, FlatbuffersCodec.

2.3. Брокер/очереди как абстракция

IBrokerAdapter

Ответственность: унифицированная оболочка над MQ (Redis, Rabbit, NATS…) или локальные очереди.

Методы: publish(topic, bytes), consume(topic, cb), ack(msgId), nack(msgId).

Варианты: InMemoryQueue, RedisStreamsAdapter, RabbitAdapter, NatsAdapter.

2.4. Передача больших данных и стриминг

IStreamChannel

Ответственность: двунаправленный поток chunk’ов, backpressure.

Методы: open(streamId), write(chunk), end(), onData(cb), onDrain(cb).

IDataMover

Ответственность: direct-transfer/peer-to-peer, сплит/слияние чанков.

Методы: sendLarge(objectRef, sink), receive(source).

IContentStore / IBlobStore

Ответственность: хранение больших бинарников (локально/дистрибутивно).

Методы: put(blob) -> contentId, get(contentId) -> stream, pin(contentId).

3. Планирование и выполнение
   3.1. Модель задач

TaskMeta / TaskId / TaskSpec

Поля: id, type, priority, affinity (geo/gpu), replicas, deadline, retries, shardInfo.

ResultMeta

Поля: taskId, status, checksum, size, partial (для стримов).

3.2. Интерфейсы планировщика

IScheduler

Ответственность: глобальная/локальная постановка в очередь, выбор исполнителя, учёт приоритетов и зависимостей.

Методы: submit(TaskSpec), schedule(), reconcile(), requeue(taskId).

IDAGEngine

Ответственность: DAG/Graph задач, топологическая сортировка, триггеры зависимостей.

Методы: addNode(task), addEdge(a,b), ready(), complete(nodeId).

3.3. Исполнение и ресурсы

IExecutorRuntime

Ответственность: запуск тасков (корутины/пул потоков), учёт CPU/GPU/памяти.

Методы: enqueue(TaskSpec) -> Future, cancel(taskId), capacity().

IResourceManager

Ответственность: трекинг ресурсов узла, квоты/SLA, NUMA/пины.

Методы: allocate(req), release(resId), utilization().

3.4. Надёжность выполнения

IRetryPolicy

Политики: backoff, maxAttempts, идемпотентность.

Методы: onFail(task, err) -> RetryDecision.

IReplication/Voting

Ответственность: параллельные копии и консенсус по результатам.

Методы: replicate(task, n), vote(results, rule) -> finalResult.

ICheckpointing

Ответственность: сохранение state долгих задач.

Методы: save(taskId, stateRef), restore(taskId).

4. Модель программирования (Developer API)
   4.1. Регистрация и вызов задач

ITaskRegistry

Ответственность: реестр типов задач/хэндлеров.

Методы: register(name, handler, codec), get(name).

IDistributedTask<TSig> (шаблон)

Ответственность: типобезопасный «удалённый вызов» с co_await.

Методы: invoke(args...) -> Future<R>, invokeStream(args...) -> IStream.

4.2. Акторы и сервисы долгого жизни

IActorRuntime

Ответственность: создание акторов, RPC-вызовы, маршрутизация к экземпляру.

Методы: spawn<TActor>(placement) -> ActorRef, ask(actorRef, method, payload).

IRPCServer / IRPCClient

Абстракция поверх протоколов (gRPC/JSON-RPC/собственный).

Методы: register(method, handler), call(nodeId, method, payload).

4.3. DAG/MapReduce/шардирование

IShardPlanner

Ответственность: разбиение данных на шард/партиции.

Методы: partition(input, policy) -> shards, coalesce(shards).

IAggregator

Ответственность: комбинирование частичных результатов.

Методы: accumulate(part), finalize().

5. Безопасность и доверие
   5.1. Идентичность и криптография

IIdentity / IKeyStore

Ответственность: ключи узла, подпись/проверка.

Методы: sign(bytes), verify(bytes, sig, pubKey), rotateKeys().

IAuthorization / IACL

Ответственность: доступ к методам/курам (multi-tenant).

Методы: check(subject, action, resource).

5.2. Шифрование и целостность

ISecureChannel

Ответственность: TLS/mTLS, ключевой обмен, PFS.

Методы: wrap(transport) -> secureTransport.

5.3. Репутация/политики доверия (опционально)

IReputationService

Ответственность: рейтинг узлов по ист. качеству/доступности.

Методы: score(nodeId), update(nodeId, event).

6. Наблюдаемость и эксплуатация
   6.1. Метрики, логирование, трассировка

IMetrics

Методы: inc(counter), observe(hist, value), gauge(set).

ILogger

Методы: log(level, msg, kv...).

ITracer

Методы: startSpan(name), inject(ctx), extract(ctx).

6.2. Админка и API управления

IAdminAPI

Ответственность: CRUD задач/политик, дашборд узлов, пауза/резюм/дренаж.

Методы: pause(nodeId), drain(nodeId), reschedule(taskId).

IConfigProvider

Ответственность: динамическая конфигурация (hot-reload).

Методы: get(key), watch(key, cb).

7. Гарантии доставки и семантика

IAcknowledgement

Ответственность: ack/nack, at-least-once.

Методы: ack(msgId), nack(msgId, requeue=true).

IIdempotencyStore

Ответственность: дедупликация по taskId/opId.

Методы: seen(opId), record(opId).

8. Планировщик времени и триггеры

IScheduleService

Ответственность: CRON/таймеры, периодические задачи.

Методы: schedule(spec, task), cancel(scheduleId).

IEventBus

Ответственность: внутренние события (taskCreated, nodeDown).

Методы: emit(event), on(eventType, cb).
