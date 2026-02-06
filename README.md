# Superbowl LX Prop Pool

A simple prop pool application for Superbowl LX.

## Running the Application

Prerequisites:
- Go
- SQLite (usually included with Go drivers)

To run the application locally:

```bash
go run .
```

The application will start on port 4884 (default) using a local `game.db` SQLite database.

Access the application at: http://localhost:4884

## Configuration

The application is configured using Environment Variables.

| Variable  | Default     | Description |
| :-------- | :---------- | :---------- |
| `PORT`    | `4884`      | The HTTP port the web server listens on. |
| `DB_NAME` | `./game.db` | The file path for the SQLite database. |

## Admin Interface

Users must be an admin first. Admin a user after they are created by restarting the server with
```bash
go run . admin <User>
```

Then to manage the game state and resolve questions, navigate to:

http://localhost:4884/admin

From here you can:
- Lock/Unlock the game (Open/Locked status).
- Resolve questions (mark correct answers).
- Refresh questions from `questions.json`.
- Edit Questions
- Manage users (reset PINs, delete users, update rooms).

## Understanding SQLite and `game.db`

This application uses **SQLite** for data storage. All user accounts, predictions, and game settings are stored in a single file: `game.db`.

* **Why is this file important?**
    It contains the entire state of the application. If this file is deleted, **all user data and scores are lost**.
* **For Docker/Deployment:**
    You **must** mount a volume to the directory containing this file. If you restart a container without a volume, the database will reset to a fresh state.

## Running the Application

### Option 1: Docker (Recommended)

1. **Build and Run:**
```bash
docker compose up -d
```

Ensure your docker-compose.yml mounts a volume to /data so game.db is persisted.
### Option 2: Local Go Development

1. **Prerequisites:**

- Go 1.23+
- GCC (Required for CGO/SQLite)

2. **Start the Server:**
```bash
go run .
```
The app will be available at http://localhost:4884.