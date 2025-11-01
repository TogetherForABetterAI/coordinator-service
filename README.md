# Coordinator Service




##  Preparacion


1. Copiar el archivo de configuración de ejemplo:

```bash
cp .env.example .env
```

2. Configurar las variables de entorno en `.env` según tu entorno

## Testing

**Ejecutar todos los tests (desde la raiz) antes de hacer push al repositorio**

### Tests Unitarios 

Ejecutar solo los tests unitarios:

```bash
go test ./...
```

### Tests de Integración 

Ejecutar solo tests de integración:

```bash
go test -v -tags=integration ./...
```




