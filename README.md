# Desafio OTEL - Sistema de Temperatura por CEP

Sistema em Go que recebe um CEP, identifica a cidade e retorna o clima atual (temperatura em Celsius, Fahrenheit e Kelvin) com tracing distribuído via OpenTelemetry e Zipkin.

## Arquitetura

- **Serviço A** (porta 8080): Recebe o CEP via POST, valida (8 dígitos) e encaminha para o Serviço B
- **Serviço B** (porta 8081): Consulta o [ViaCEP](https://viacep.com.br/) para obter a cidade, consulta o [WeatherAPI](https://www.weatherapi.com/) para obter a temperatura e retorna os dados formatados em Celsius, Fahrenheit e Kelvin
- **OTEL Collector**: Recebe os traces via gRPC (porta 4317) e exporta para o Zipkin
- **Zipkin**: Interface para visualização dos traces distribuídos (porta 9411)

## Pré-requisitos

- [Docker](https://www.docker.com/) e [Docker Compose](https://docs.docker.com/compose/) instalados

## Como rodar

```bash
docker compose up --build
```

Aguarde todos os serviços iniciarem. O Serviço A estará disponível em `http://localhost:8080`.

## Como testar

### CEP válido

```bash
curl -s -X POST http://localhost:8080/service-a \
  -H "Content-Type: application/json" \
  -d '{"cep": "87043480"}'
```

Resposta esperada (HTTP 200):

```json
{
  "city": "Maringá",
  "temp_C": 28.5,
  "temp_F": 83.3,
  "temp_K": 301.5
}
```

### CEP inválido (formato incorreto)

```bash
curl -s -X POST http://localhost:8080/service-a \
  -H "Content-Type: application/json" \
  -d '{"cep": "123"}'
```

Resposta esperada (HTTP 422):

```json
{
  "message": "invalid zipcode"
}
```

### CEP não encontrado

```bash
curl -s -X POST http://localhost:8080/service-a \
  -H "Content-Type: application/json" \
  -d '{"cep": "00000000"}'
```

Resposta esperada (HTTP 404):

```json
{
  "message": "can not find zipcode"
}
```

## Visualizar traces

Acesse o Zipkin em: [http://localhost:9411](http://localhost:9411)

