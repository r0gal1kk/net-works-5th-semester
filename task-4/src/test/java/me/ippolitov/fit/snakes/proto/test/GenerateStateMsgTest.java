package me.ippolitov.fit.snakes.proto.test;

import me.ippolitov.fit.snakes.SnakesProto.GameMessage.StateMsg;
import me.ippolitov.fit.snakes.SnakesProto.GameState.Snake;
import me.ippolitov.fit.snakes.SnakesProto.GameState.Snake.SnakeState;
import org.junit.Assert;
import org.junit.Test;

import static me.ippolitov.fit.snakes.SnakesProto.*;

public class GenerateStateMsgTest {
    /** Генерирует пример сообщения с состоянием поля, соответствующим картинке example1.png */
    @Test
    public void testGenerateStateMsg() {
        Snake snake = Snake.newBuilder()
                .setPlayerId(1)
                .setHeadDirection(Direction.LEFT)
                .setState(SnakeState.ALIVE)
                .addPoints(coord(5, 1)) // голова
                .addPoints(coord(3, 0))
                .addPoints(coord(0, 2))
                .addPoints(coord(-4, 0))
                .build();
        // Единственный игрок в игре, он же MASTER
        GamePlayer playerBob = GamePlayer.newBuilder()
                .setId(1)
                .setRole(NodeRole.MASTER)
                // MASTER не отправляет собственные IP и порт
                .setName("Bob")
                .setScore(8)
                .build();
        GamePlayers players = GamePlayers.newBuilder()
                .addPlayers(playerBob)
                .build();
        GameState state = GameState.newBuilder()
                .setStateOrder(193)
                .addSnakes(snake)
                .setPlayers(players)
                .addFoods(coord(7, 6))
                .addFoods(coord(8, 7))
                .build();
        StateMsg stateMsg = StateMsg.newBuilder()
                .setState(state)
                .build();
        GameMessage gameMessage = GameMessage.newBuilder()
                .setMsgSeq(15643)
                .setState(stateMsg)
                .build();

        byte[] bytesToSendViaDatagramPacket = gameMessage.toByteArray();
        Assert.assertEquals(69, bytesToSendViaDatagramPacket.length);
    }

    private GameState.Coord coord(int x, int y) {
        return GameState.Coord.newBuilder().setX(x).setY(y).build();
    }
}
